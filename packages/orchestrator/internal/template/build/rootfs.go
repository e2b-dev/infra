package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dustin/go-humanize"
	"github.com/google/go-containerregistry/pkg/crane"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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

	networkPool   *network.Pool
	templateCache *template.Cache
	devicePool    *nbd.DevicePool
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
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	template *TemplateConfig,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
) *Rootfs {
	return &Rootfs{
		template:         template,
		artifactRegistry: artifactRegistry,
		networkPool:      networkPool,
		templateCache:    templateCache,
		devicePool:       devicePool,
	}
}

func (r *Rootfs) createExt4Filesystem(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	rootfsPath string,
) (c containerregistry.Config, e error) {
	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, "failed to create ext4 filesystem", e)
		}
	}()

	postProcessor.WriteMsg("Requesting Docker Image")

	var img containerregistry.Image
	var err error
	if r.template.FromImage != "" {
		img, err = oci.GetPublicImage(childCtx, tracer, r.template.FromImage)
	} else {
		img, err = oci.GetImage(childCtx, tracer, r.artifactRegistry, r.template.TemplateId, r.template.BuildId)
	}
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error requesting docker image: %w", err)
	}

	imageSize, err := oci.GetImageSize(img)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting image size: %w", err)
	}
	postProcessor.WriteMsg(fmt.Sprintf("Docker image size: %s", humanize.Bytes(uint64(imageSize))))

	// Template build layers
	imagePath, err := r.todoBuildLayers(ctx, tracer, postProcessor, img)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error building layers: %w", err)
	}
	defer os.Remove(imagePath)

	img, err = tarball.ImageFromPath(imagePath, nil)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error requesting docker image: %w", err)
	}
	// Template build layers

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
	ext4Size, err := oci.ToExt4(ctx, tracer, postProcessor, img, rootfsPath, maxRootfsSize, r.template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error creating ext4 filesystem: %w", err)
	}
	r.template.rootfsSize = ext4Size
	telemetry.ReportEvent(childCtx, "created rootfs ext4 file")

	postProcessor.WriteMsg("Filesystem cleanup")
	// Make rootfs writable, be default it's readonly
	err = ext4.MakeWritable(ctx, tracer, rootfsPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error making rootfs file writable: %w", err)
	}

	// Resize rootfs
	rootfsFreeSpace, err := ext4.GetFreeSpace(ctx, tracer, rootfsPath, r.template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting free space: %w", err)
	}
	// We need to remove the remaining free space from the ext4 file size
	// This is a residual space that could not be shrunk when creating the filesystem,
	// but is still available for use
	diskAdd := r.template.DiskSizeMB<<ToMBShift - rootfsFreeSpace
	zap.L().Debug("adding disk size diff to rootfs",
		zap.Int64("size_current", ext4Size),
		zap.Int64("size_add", diskAdd),
		zap.Int64("size_free", rootfsFreeSpace),
	)
	if diskAdd > 0 {
		rootfsFinalSize, err := ext4.Enlarge(ctx, tracer, rootfsPath, diskAdd)
		if err != nil {
			return containerregistry.Config{}, fmt.Errorf("error enlarging rootfs: %w", err)
		}
		r.template.rootfsSize = rootfsFinalSize
	}

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

	hosts := fmt.Sprintf(`127.0.0.1	localhost
::1	localhost ip6-localhost ip6-loopback
fe00::	ip6-localnet
ff00::	ip6-mcastprefix
ff02::1	ip6-allnodes
ff02::2	ip6-allrouters
127.0.1.1	%s
`, hostname)

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

func (r *Rootfs) todoBuildLayers(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	img containerregistry.Image,
) (path string, e error) {
	ctx, span := tracer.Start(ctx, "build-layers")
	defer span.End()

	// Start Virtual Dagger runner
	config := &orchestrator.SandboxConfig{
		TemplateId:         "p9kw2u9cc1zj1cov2zru",
		BuildId:            "6bea9b8c-7344-4e8d-bfdc-16b10876606c",
		KernelVersion:      "vmlinux-6.1.102",
		FirecrackerVersion: "v1.10.1_1fcdaec",
		HugePages:          true,
		SandboxId:          instanceBuildPrefix + id.Generate(),
		ExecutionId:        uuid.New().String(),
		EnvdVersion:        "0.2.0",
		Vcpu:               8,
		RamMb:              8 * 1024,

		BaseTemplateId: "p9kw2u9cc1zj1cov2zru",
	}
	sbx, cleanup, err := sandbox.ResumeSandbox(
		ctx,
		tracer,
		nil,
		r.networkPool,
		r.templateCache,
		config,
		uuid.New().String(),
		time.Now(),
		time.Now().Add(60*time.Minute),
		"p9kw2u9cc1zj1cov2zru",
		r.devicePool,
		true,
		false,
	)
	defer func() {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			e = errors.Join(e, fmt.Errorf("error cleaning up sandbox: %w", cleanupErr))
		}
	}()
	if err != nil {
		return "", fmt.Errorf("error creating sandbox: %w", err)
	}
	// Start Virtual Dagger runner

	err = os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", fmt.Sprintf("tcp://%s:1234", sbx.Slot.HostIPString()))
	if err != nil {
		return "", fmt.Errorf("failed to set Dagger environment variable: %w", err)
	}

	logsBuffer := &bytes.Buffer{}
	defer func() {
		zap.L().Debug("Dagger logs",
			zap.String("logs", logsBuffer.String()),
			zap.Int("length", logsBuffer.Len()),
		)
	}()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logsBuffer))
	if err != nil {
		return "", fmt.Errorf("failed to connect to Dagger: %w", err)
	}
	defer client.Close()

	layerSourceImage, err := os.CreateTemp("", "layer-image-*.tar")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer layerSourceImage.Close()

	err = crane.Save(img, uuid.New().String(), layerSourceImage.Name())
	if err != nil {
		return "", fmt.Errorf("failed to write source image to temporary file: %w", err)
	}
	layerSourceImagePath := layerSourceImage.Name()

	for i, step := range r.template.Steps {
		err := func() error {
			defer os.Remove(layerSourceImagePath)

			layerOutputImage, err := os.CreateTemp("", "layer-image-*.tar")
			if err != nil {
				return fmt.Errorf("failed to create temporary file: %w", err)
			}
			defer layerOutputImage.Close()
			layerOutputImagePath := layerOutputImage.Name()

			cmd := fmt.Sprintf("%s %s", step.Type, strings.Join(step.Args, " "))
			zap.L().Debug("building layer",
				zap.String("source_file_path", layerSourceImagePath),
				zap.String("target_file_path", layerOutputImagePath),
				zap.String("command", cmd),
			)

			cached := ""
			if false {
				cached = "CACHED "
			}
			prefix := fmt.Sprintf("[builder %d/%d]", i+1, len(r.template.Steps))
			postProcessor.WriteMsg(fmt.Sprintf("%s%s: %s", cached, prefix, cmd))
			hash, err := r.buildLayer(
				ctx,
				tracer,
				postProcessor,
				client,
				prefix,
				layerSourceImagePath,
				layerOutputImagePath,
				img,
				cmd,
			)
			if err != nil {
				return err
			}

			zap.L().Debug("built layer",
				zap.String("layer_hash", hash),
				zap.String("layer_source_image", layerSourceImagePath),
				zap.String("layer_output_image", layerOutputImagePath),
			)

			layerSourceImagePath = layerOutputImagePath
			return nil
		}()
		if err != nil {
			return "", fmt.Errorf("error building layer %d: %w", i+1, err)
		}
	}

	return layerSourceImagePath, nil
}

func (r *Rootfs) buildLayer(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	client *dagger.Client,
	prefix string,
	sourceFilePath string,
	targetFilePath string,
	img containerregistry.Image,
	command string,
) (string, error) {
	ctx, span := tracer.Start(ctx, "build-layer")
	defer span.End()

	sourceLayer := client.Host().File(sourceFilePath)
	container := client.Container().
		Import(sourceLayer).
		WithExec([]string{"sh", "-c", command})

	stderr, err := container.Stderr(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get container stderr: %w", err)
	}
	stdout, err := container.Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get container stdout: %w", err)
	}

	if stderr != "" {
		postProcessor.WriteMsg(fmt.Sprintf("%s [stderr]: %s", prefix, stderr))
	}
	if stdout != "" {
		postProcessor.WriteMsg(fmt.Sprintf("%s [stdout]: %s", prefix, stdout))
	}

	zap.L().Debug("container output",
		zap.String("stdout", stdout),
		zap.String("stderr", stderr),
	)

	tar := container.AsTarball()
	export, err := tar.Export(ctx, targetFilePath)
	if err != nil {
		return "", err
	}

	zap.L().Debug("exported layer",
		zap.String("source_file_path", sourceFilePath),
		zap.String("target_file_path", targetFilePath),
		zap.String("command", command),
		zap.String("export", export),
	)

	img, err = tarball.ImageFromPath(targetFilePath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get image from Dagger: %w", err)
	}

	hash := uuid.New().String()
	err = r.artifactRegistry.PushLayer(ctx, r.template.BuildId, hash, img)
	if err != nil {
		return "", err
	}

	zap.L().Debug("pushed layer",
		zap.String("source_file_path", sourceFilePath),
		zap.String("target_file_path", targetFilePath),
		zap.String("command", command),
	)

	return hash, nil
}
