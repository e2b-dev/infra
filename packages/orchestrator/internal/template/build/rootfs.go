package build

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/dustin/go-humanize"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ToMBShift = 20
	// Max size of the rootfs file in MB.
	maxRootfsSize = 15000 << ToMBShift

	rootfsBuildFileName = "rootfs.ext4.build"
	rootfsProvisionLink = "rootfs.ext4.build.provision"

	// provisionScriptFileName is a path where the provision script stores it's exit code.
	provisionScriptResultPath = "/provision.result"
	logExternalPrefix         = "[external] "

	busyBoxBinaryPath = "/bin/busybox"
	busyBoxInitPath   = "usr/bin/init"
	systemdInitPath   = "/sbin/init"
)

type Rootfs struct {
	template         *TemplateConfig
	artifactRegistry artifactsregistry.ArtifactsRegistry
}

type MultiWriter struct {
	writers []io.Writer
}

func (mw *MultiWriter) Write(p []byte) (int, error) {
	for _, writer := range mw.writers {
		_, err := writer.Write(p)
		if err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

func NewRootfs(artifactRegistry artifactsregistry.ArtifactsRegistry, template *TemplateConfig) *Rootfs {
	return &Rootfs{
		template:         template,
		artifactRegistry: artifactRegistry,
	}
}

func (r *Rootfs) createExt4Filesystem(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, rootfsPath string) (c containerregistry.Config, e error) {
	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, "failed to create ext4 filesystem", e)
		}
	}()

	postProcessor.WriteMsg("Requesting Docker Image")

	img, err := oci.GetImage(childCtx, tracer, r.artifactRegistry, r.template.TemplateId, r.template.BuildId)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error requesting docker image: %w", err)
	}

	imageSize, err := oci.GetImageSize(img)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting image size: %w", err)
	}
	postProcessor.WriteMsg(fmt.Sprintf("Docker image size: %s", humanize.Bytes(uint64(imageSize))))

	postProcessor.WriteMsg("Setting up system files")
	layers, err := additionalOCILayers(childCtx, r.template)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error populating filesystem: %w", err)
	}
	img, err = mutate.AppendLayers(img, layers...)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error appending layers: %w", err)
	}
	telemetry.ReportEvent(childCtx, "set up filesystem")

	postProcessor.WriteMsg("Creating file system and pulling Docker image")
	err = oci.ToExt4(ctx, img, rootfsPath, maxRootfsSize)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error creating ext4 filesystem: %w", err)
	}
	telemetry.ReportEvent(childCtx, "created rootfs ext4 file")

	postProcessor.WriteMsg("Filesystem cleanup")
	// Make rootfs writable, be default it's readonly
	err = ext4.MakeWritable(ctx, tracer, rootfsPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error making rootfs file writable: %w", err)
	}

	// Resize rootfs
	rootfsFinalSize, err := ext4.Enlarge(ctx, tracer, rootfsPath, r.template.DiskSizeMB<<ToMBShift)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error enlarging rootfs: %w", err)
	}
	r.template.rootfsSize = rootfsFinalSize

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
	zap.L().Debug("filesystem ext4 integrity",
		zap.String("result", ext4Check),
		zap.Error(err),
	)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error checking ext4 filesystem integrity: %w", err)
	}

	config, err := img.ConfigFile()
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting image config file: %w", err)
	}

	return config.Config, nil
}

func additionalOCILayers(
	ctx context.Context,
	config *TemplateConfig,
) ([]containerregistry.Layer, error) {
	var scriptDef bytes.Buffer
	err := ProvisionScriptTemplate.Execute(&scriptDef, struct {
		ResultPath string
	}{
		ResultPath: provisionScriptResultPath,
	})
	if err != nil {
		return nil, fmt.Errorf("error executing provision script: %w", err)
	}
	telemetry.ReportEvent(ctx, "executed provision script env")

	memoryLimit := int(math.Min(float64(config.MemoryMB)/2, 512))
	envdService := fmt.Sprintf(`[Unit]
Description=Env Daemon Service
After=multi-user.target

[Service]
Type=simple
Restart=always
User=root
Group=root
Environment=GOTRACEBACK=all
LimitCORE=infinity
ExecStart=/bin/bash -l -c "/usr/bin/envd"
OOMPolicy=continue
OOMScoreAdjust=-1000
Environment="GOMEMLIMIT=%dMiB"

[Install]
WantedBy=multi-user.target
`, memoryLimit)

	autologinService := `[Service]
ExecStart=
ExecStart=-/sbin/agetty --noissue --autologin root %I 115200,38400,9600 vt102
`

	hostname := "e2b.local"
	eventProxyHostname := "event.e2b.com"

	hosts := fmt.Sprintf(`127.0.0.1	localhost
::1	localhost ip6-localhost ip6-loopback
fe00::	ip6-localnet
ff00::	ip6-mcastprefix
ff02::1	ip6-allnodes
ff02::2	ip6-allrouters
127.0.1.1	%s
10.20.7.67	%s
`, hostname, eventProxyHostname)

	e2bFile := fmt.Sprintf(`ENV_ID=%s
BUILD_ID=%s
`, config.TemplateId, config.BuildId)

	envdFileData, err := os.ReadFile(storage.HostEnvdPath)
	if err != nil {
		return nil, fmt.Errorf("error reading envd file: %w", err)
	}

	busyBox, err := os.ReadFile(busyBoxBinaryPath)
	if err != nil {
		return nil, fmt.Errorf("error reading busybox binary: %w", err)
	}

	filesLayer, err := LayerFile(
		map[string]layerFile{
			// Setup system
			"etc/hostname":    {[]byte(hostname), 0o644},
			"etc/hosts":       {[]byte(hosts), 0o644},
			"etc/resolv.conf": {[]byte("nameserver 8.8.8.8"), 0o644},

			".e2b":                            {[]byte(e2bFile), 0o644},
			storage.GuestEnvdPath:             {envdFileData, 0o777},
			"etc/systemd/system/envd.service": {[]byte(envdService), 0o644},
			"etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf": {[]byte(autologinService), 0o644},

			// Provision script
			"usr/local/bin/provision.sh": {scriptDef.Bytes(), 0o777},
			// Setup init system
			"usr/bin/busybox": {busyBox, 0o755},
			// Set to bin/init so it's not in conflict with systemd
			// Any rewrite of the init file when booted from it will corrupt the filesystem
			busyBoxInitPath: {busyBox, 0o755},
			"etc/init.d/rcS": {[]byte(`#!/usr/bin/busybox ash
echo "Mounting essential filesystems"
# Ensure necessary mount points exist
mkdir -p /proc /sys /dev /tmp /run

# Mount essential filesystems
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /run

echo "System Init"`), 0o777},
			"etc/inittab": {[]byte(fmt.Sprintf(`# Run system init
::sysinit:/etc/init.d/rcS

# Run the provision script, prefix the output with a log prefix
::wait:/bin/sh -c '/usr/local/bin/provision.sh 2>&1 | sed "s/^/%s/"'

# Reboot the system after the script
# Running the poweroff or halt commands inside a Linux guest will bring it down but Firecracker process remains unaware of the guest shutdown so it lives on.
# Running the reboot command in a Linux guest will gracefully bring down the guest system and also bring a graceful end to the Firecracker process.
::once:/usr/bin/busybox reboot

# Clean shutdown of filesystems and swap
::shutdown:/usr/bin/busybox swapoff -a
::shutdown:/usr/bin/busybox umount -a -r -v
`, logExternalPrefix)), 0o777},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating layer from files: %w", err)
	}

	symlinkLayer, err := LayerSymlink(
		map[string]string{
			// Enable envd service autostart
			"etc/systemd/system/multi-user.target.wants/envd.service": "etc/systemd/system/envd.service",
			// Enable chrony service autostart
			"etc/systemd/system/multi-user.target.wants/chrony.service": "etc/systemd/system/chrony.service",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating layer from symlinks: %w", err)
	}

	return []containerregistry.Layer{
		filesLayer,
		symlinkLayer,
	}, nil
}
