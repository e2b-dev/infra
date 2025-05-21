package build

import (
	"bytes"
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ToMBShift = 20
	// Max size of the rootfs file in MB.
	maxRootfsSize = 15000 << ToMBShift

	rootfsBuildFileName = "rootfs.ext4.build"
	rootfsProvisionLink = "rootfs.ext4.build.provision"

	busyBoxInitPath = "/bin/init"
	systemdInitPath = "/sbin/init"
)

type Rootfs struct {
	template *TemplateConfig
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

func NewRootfs(
	template *TemplateConfig,
) *Rootfs {
	return &Rootfs{
		template: template,
	}
}
func (r *Rootfs) dockerTag() string {
	return fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s", consts.GCPRegion, consts.GCPProject, consts.DockerRegistry, r.template.TemplateId, r.template.BuildId)
}

func (r *Rootfs) createFilesystemFromDocker(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor) (string, error) {
	childCtx, span := tracer.Start(ctx, "buildah-rootfs")
	defer span.End()

	postProcessor.WriteMsg("Pulling Docker image")
	image, err := oci.PullImage(childCtx, tracer, r.dockerTag())
	if err != nil {
		return "", fmt.Errorf("failed to pull image: %w", err)
	}

	postProcessor.WriteMsg("Mounting image filesystem")
	mountPath, err := oci.MountImage(childCtx, tracer, image)
	if err != nil {
		return "", fmt.Errorf("failed to mount image: %w", err)
	}

	postProcessor.WriteMsg("Injecting additional system files at " + mountPath)
	if err := injectFilesToMount(mountPath, r.template); err != nil {
		return "", fmt.Errorf("injecting system files failed: %w", err)
	}

	return mountPath, nil
}

func (r *Rootfs) createExt4Filesystem(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, rootfsPath string) (e error) {
	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, e)
		}
	}()

	mount, err := r.createFilesystemFromDocker(ctx, tracer, postProcessor)
	if err != nil {
		return fmt.Errorf("failed to create OCI filesystem: %w", err)
	}

	start := time.Now()
	postProcessor.WriteMsg("Creating file system from Docker image")
	err = oci.ToExt4FromMount(ctx, mount, rootfsPath, maxRootfsSize)
	postProcessor.WriteMsg("CreateFS took: " + time.Since(start).String())
	if err != nil {
		return fmt.Errorf("error creating filesystem: %w", err)
	}
	err = ext4.Shrink(ctx, rootfsPath)
	if err != nil {
		return fmt.Errorf("error shrinking filesystem: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created rootfs ext4 file")
	return fmt.Errorf("temporary exit")

	//postProcessor.WriteMsg("Requesting Docker Image")
	//img, err := oci.GetImage(childCtx, tracer, r.dockerTag())make
	//if err != nil {
	//	return fmt.Errorf("error requesting docker image: %w", err)
	//}
	//
	//imageSize, err := oci.GetImageSize(img)
	//if err != nil {
	//	return fmt.Errorf("error getting image size: %w", err)
	//}
	//postProcessor.WriteMsg(fmt.Sprintf("Docker image size: %s", humanize.Bytes(uint64(imageSize))))
	//
	//postProcessor.WriteMsg("Setting up system files")
	//layers, err := additionalOCILayers(childCtx, r.template)
	//if err != nil {
	//	return fmt.Errorf("error populating filesystem: %w", err)
	//}
	//img, err = mutate.AppendLayers(img, layers...)
	//if err != nil {
	//	return fmt.Errorf("error appending layers: %w", err)
	//}
	//telemetry.ReportEvent(childCtx, "set up filesystem")

	//postProcessor.WriteMsg("Creating file system and pulling Docker image")
	//err = oci.ToExt4(ctx, img, rootfsPath, maxRootfsSize)
	//if err != nil {
	//	return fmt.Errorf("error creating ext4 filesystem: %w", err)
	//}
	//telemetry.ReportEvent(childCtx, "created rootfs ext4 file")

	postProcessor.WriteMsg("Filesystem cleanup")
	// Make rootfs writable, be default it's readonly
	err = ext4.MakeWritable(ctx, tracer, rootfsPath)
	if err != nil {
		return fmt.Errorf("error making rootfs file writable: %w", err)
	}

	// Resize rootfs
	rootfsFinalSize, err := ext4.Enlarge(ctx, tracer, rootfsPath, r.template.DiskSizeMB<<ToMBShift)
	if err != nil {
		return fmt.Errorf("error enlarging rootfs: %w", err)
	}
	r.template.rootfsSize = rootfsFinalSize

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
	zap.L().Debug("filesystem ext4 integrity",
		zap.String("result", ext4Check),
		zap.Error(err),
	)
	if err != nil {
		return fmt.Errorf("error checking ext4 filesystem integrity: %w", err)
	}

	return nil
}

func additionalOCILayers(
	ctx context.Context,
	config *TemplateConfig,
) ([]v1.Layer, error) {
	var scriptDef bytes.Buffer
	err := ProvisionScriptTemplate.Execute(&scriptDef, struct{}{})
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

	hosts := fmt.Sprintf(`127.0.0.1   localhost
127.0.1.1   %s
`, hostname)

	e2bFile := fmt.Sprintf(`ENV_ID=%s
BUILD_ID=%s
`, config.TemplateId, config.BuildId)

	envdFileData, err := os.ReadFile(storage.HostEnvdPath)
	if err != nil {
		return nil, fmt.Errorf("error reading envd file: %w", err)
	}

	busyBox, err := os.ReadFile("/bin/busybox")
	if err != nil {
		return nil, fmt.Errorf("error reading busybox: %w", err)
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
			"usr" + busyBoxInitPath: {busyBox, 0o755},
			"etc/init.d/rcS": {[]byte(`#!/usr/bin/busybox ash
echo "Mounting essential filesystems"
# Ensure necessary mount points exist
mkdir -p /proc /sys /dev

# Mount essential filesystems
mount -t proc proc /proc
mount -t sysfs sysfs /sys

echo "System Init"`), 0o777},
			"etc/inittab": {[]byte(`# Run system init
::sysinit:/etc/init.d/rcS

# Run the provision script
::wait:/usr/local/bin/provision.sh

# Reboot the system after the script
# Running the poweroff or halt commands inside a Linux guest will bring it down but Firecracker process remains unaware of the guest shutdown so it lives on.
# Running the reboot command in a Linux guest will gracefully bring down the guest system and also bring a graceful end to the Firecracker process.
::once:/usr/bin/busybox reboot
::shutdown:/usr/bin/busybox umount -a -r
`), 0o777},
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

	return []v1.Layer{
		filesLayer,
		symlinkLayer,
	}, nil
}

func injectFilesToMount(rootfs string, config *TemplateConfig) error {
	// Generate the provision script
	var scriptDef bytes.Buffer
	err := ProvisionScriptTemplate.Execute(&scriptDef, struct{}{})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}
	telemetry.ReportEvent(context.TODO(), "executed provision script env")

	// Prepare memory limit and systemd service
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
	hosts := fmt.Sprintf(`127.0.0.1   localhost
127.0.1.1   %s
`, hostname)

	e2bFile := fmt.Sprintf(`ENV_ID=%s
BUILD_ID=%s
`, config.TemplateId, config.BuildId)

	// Read content from host
	envdFileData, err := os.ReadFile(storage.HostEnvdPath)
	if err != nil {
		return fmt.Errorf("error reading envd file: %w", err)
	}
	busyBox, err := os.ReadFile("/bin/busybox")
	if err != nil {
		return fmt.Errorf("error reading busybox: %w", err)
	}

	// Define files to write
	files := map[string]struct {
		Content []byte
		Perm    os.FileMode
	}{
		"etc/hostname":                    {[]byte(hostname), 0o644},
		"etc/hosts":                       {[]byte(hosts), 0o644},
		"etc/resolv.conf":                 {[]byte("nameserver 8.8.8.8"), 0o644},
		".e2b":                            {[]byte(e2bFile), 0o644},
		storage.GuestEnvdPath:             {envdFileData, 0o777},
		"etc/systemd/system/envd.service": {[]byte(envdService), 0o644},
		"etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf": {[]byte(autologinService), 0o644},
		"usr/local/bin/provision.sh":                                     {scriptDef.Bytes(), 0o777},
		"usr/bin/busybox":                                                {busyBox, 0o755},
		"usr" + busyBoxInitPath:                                          {busyBox, 0o755},
		"etc/init.d/rcS": {[]byte(`#!/usr/bin/busybox ash
echo "Mounting essential filesystems"
mkdir -p /proc /sys /dev
mount -t proc proc /proc
mount -t sysfs sysfs /sys
echo "System Init"`), 0o777},
		"etc/inittab": {[]byte(`# Run system init
::sysinit:/etc/init.d/rcS
::wait:/usr/local/bin/provision.sh
::once:/usr/bin/busybox reboot
::shutdown:/usr/bin/busybox umount -a -r
`), 0o777},
	}

	// Write all files
	for relPath, file := range files {
		fullPath := filepath.Join(rootfs, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", relPath, err)
		}
		if err := os.WriteFile(fullPath, file.Content, file.Perm); err != nil {
			return fmt.Errorf("failed to write %s: %w", relPath, err)
		}
	}

	// Create symlinks
	symlinks := map[string]string{
		"etc/systemd/system/multi-user.target.wants/envd.service":   "/etc/systemd/system/envd.service",
		"etc/systemd/system/multi-user.target.wants/chrony.service": "/etc/systemd/system/chrony.service",
	}

	for link, target := range symlinks {
		linkPath := filepath.Join(rootfs, link)
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for symlink %s: %w", link, err)
		}
		if err := utils.SymlinkForce(target, linkPath); err != nil {
			return fmt.Errorf("failed to create symlink %s -> %s: %w", link, target, err)
		}
	}

	return nil
}
