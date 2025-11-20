package rootfs

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/dustin/go-humanize"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/systeminit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs")

const (
	// Max size of the rootfs file in MB.
	maxRootfsSize = 25000 << constants.ToMBShift

	BusyBoxPath     = "usr/bin/busybox"
	BusyBoxInitPath = "usr/bin/init"

	ProvisioningExitPrefix = "E2B_PROVISIONING_EXIT:"

	serviceWatchDogDisabledConfig = `[Service]
WatchdogSec=0
`
)

type Rootfs struct {
	metadata            storage.TemplateFiles
	template            config.TemplateConfig
	artifactRegistry    artifactsregistry.ArtifactsRegistry
	dockerhubRepository dockerhub.RemoteRepository
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

func New(
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	dockerhubRepository dockerhub.RemoteRepository,
	metadata storage.TemplateFiles,
	template config.TemplateConfig,
) *Rootfs {
	return &Rootfs{
		metadata:            metadata,
		template:            template,
		artifactRegistry:    artifactRegistry,
		dockerhubRepository: dockerhubRepository,
	}
}

func (r *Rootfs) CreateExt4Filesystem(
	ctx context.Context,
	logger *zap.Logger,
	rootfsPath string,
	provisionScript string,
	provisionLogPrefix string,
	provisionResultPath string,
) (c containerregistry.Config, e error) {
	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, "failed to create ext4 filesystem", e)
		}
	}()

	logger.Debug("Requesting Docker Image")

	var img containerregistry.Image
	var err error
	if r.template.FromImage != "" {
		img, err = oci.GetPublicImage(childCtx, r.dockerhubRepository, r.template.FromImage, r.template.RegistryAuthProvider)
	} else {
		img, err = oci.GetImage(childCtx, r.artifactRegistry, r.template.TemplateID, r.metadata.BuildID)
	}
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error requesting docker image: %w", err)
	}

	imageSize, err := oci.GetImageSize(img)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting image size: %w", err)
	}
	logger.Info(fmt.Sprintf("Base Docker image size: %s", humanize.Bytes(uint64(imageSize))))

	logger.Debug("Setting up system files")
	layers, err := additionalOCILayers(childCtx, r.template, provisionScript, provisionLogPrefix, provisionResultPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error populating filesystem: %w", err)
	}
	img, err = mutate.AppendLayers(img, layers...)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error appending layers: %w", err)
	}
	telemetry.ReportEvent(childCtx, "set up filesystem")

	logger.Info("Creating file system and pulling Docker image")
	ext4Size, err := oci.ToExt4(ctx, logger, img, rootfsPath, maxRootfsSize, r.template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error converting oci to ext4: %w", err)
	}
	telemetry.ReportEvent(childCtx, "created rootfs ext4 file")

	logger.Debug("Filesystem cleanup")
	// Make rootfs writable, be default it's readonly
	err = filesystem.MakeWritable(ctx, rootfsPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error making rootfs file writable: %w", err)
	}

	// Resize rootfs
	rootfsFreeSpace, err := filesystem.GetFreeSpace(ctx, rootfsPath, r.template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting free space: %w", err)
	}
	// We need to remove the remaining free space from the ext4 file size
	// This is a residual space that could not be shrunk when creating the filesystem,
	// but is still available for use
	diskAdd := r.template.DiskSizeMB<<constants.ToMBShift - rootfsFreeSpace
	zap.L().Debug("adding disk size diff to rootfs",
		zap.Int64("size_current", ext4Size),
		zap.Int64("size_add", diskAdd),
		zap.Int64("size_free", rootfsFreeSpace),
	)
	if diskAdd > 0 {
		_, err := filesystem.Enlarge(ctx, rootfsPath, diskAdd)
		if err != nil {
			return containerregistry.Config{}, fmt.Errorf("error enlarging rootfs: %w", err)
		}
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := filesystem.CheckIntegrity(ctx, rootfsPath, true)
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
	_ context.Context,
	config config.TemplateConfig,
	provisionScript string,
	provisionLogPrefix string,
	provisionResultPath string,
) ([]containerregistry.Layer, error) {
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

	hosts := fmt.Sprintf(`127.0.0.1	localhost
::1	localhost ip6-localhost ip6-loopback
fe00::	ip6-localnet
ff00::	ip6-mcastprefix
ff02::1	ip6-allnodes
ff02::2	ip6-allrouters
127.0.1.1	%s
`, hostname)

	envdFileData, err := os.ReadFile(storage.HostEnvdPath())
	if err != nil {
		return nil, fmt.Errorf("error reading envd file: %w", err)
	}

	filesLayer, err := oci.LayerFile(
		map[string]oci.File{
			// Setup system
			"etc/hostname":    {Bytes: []byte(hostname), Mode: 0o644},
			"etc/hosts":       {Bytes: []byte(hosts), Mode: 0o644},
			"etc/resolv.conf": {Bytes: []byte("nameserver 8.8.8.8"), Mode: 0o644},

			storage.GuestEnvdPath:                                            {Bytes: envdFileData, Mode: 0o777},
			"etc/systemd/system/envd.service":                                {Bytes: []byte(envdService), Mode: 0o644},
			"etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf": {Bytes: []byte(autologinService), Mode: 0o644},
			"etc/systemd/system/systemd-journald.service.d/override.conf":    {Bytes: []byte(serviceWatchDogDisabledConfig), Mode: 0o644},
			"etc/systemd/system/systemd-networkd.service.d/override.conf":    {Bytes: []byte(serviceWatchDogDisabledConfig), Mode: 0o644},

			// Provision script
			"usr/local/bin/provision.sh": {Bytes: []byte(provisionScript), Mode: 0o777},
			// Setup init system
			BusyBoxPath: {Bytes: systeminit.BusyboxBinary, Mode: 0o755},
			// Set to bin/init so it's not in conflict with systemd
			// Any rewrite of the init file when booted from it will corrupt the filesystem
			BusyBoxInitPath: {Bytes: systeminit.BusyboxBinary, Mode: 0o755},
			"etc/init.d/rcS": {Bytes: []byte(`#!/usr/bin/busybox ash
echo "Mounting essential filesystems"
# Ensure necessary mount points exist
mkdir -p /proc /sys /dev /tmp /run

# Mount essential filesystems
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /run

echo "System Init"`), Mode: 0o777},
			"etc/inittab": {Bytes: fmt.Appendf(nil, `# Run system init
::sysinit:/etc/init.d/rcS

# Run the provision script, prefix the output with a log prefix
::wait:/bin/sh -c '/usr/local/bin/provision.sh 2>&1 | sed "s/^/%s/"'

# Flush filesystem changes to disk
::wait:/usr/bin/busybox sync

# Report the exit code of the provisioning script
::wait:/bin/sh -c 'echo "%s$(cat %s || printf 1)"'

# Wait forever to prevent the VM from exiting until the sandbox is paused and snapshot is taken
::wait:/usr/bin/busybox sleep infinity
`, provisionLogPrefix, ProvisioningExitPrefix, provisionResultPath), Mode: 0o777},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating layer from files: %w", err)
	}

	symlinkLayer, err := oci.LayerSymlink(
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
