package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

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

func (r *Rootfs) createExt4Filesystem(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, rootfsPath string) (e error) {
	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, e)
		}
	}()

	postProcessor.WriteMsg("Requesting Docker Image")
	img, err := getOCIImage(childCtx, tracer, r.dockerTag())
	if err != nil {
		return fmt.Errorf("error requesting docker image: %w", err)
	}

	postProcessor.WriteMsg("Provisioning template")
	var scriptDef bytes.Buffer
	memoryLimit := int(math.Min(float64(r.env.MemoryMB)/2, 512))
	err = EnvInstanceTemplate.Execute(&scriptDef, struct{}{})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}
	telemetry.ReportEvent(childCtx, "executed provision script env")

	postProcessor.WriteMsg("Setting up system files")
	provisionService := `[Unit]
Description=Provision Script
DefaultDependencies=no
Before=network-pre.target network.target network-online.target sysinit.target systemd-hostnamed.service systemd-resolved.service
Wants=network-pre.target
Requires=local-fs.target
After=local-fs.target
OnFailure=emergency.target

[Service]
Type=oneshot
ExecStart=/provision.sh
RemainAfterExit=true

# Fail boot if this service fails
StandardError=journal
TimeoutStartSec=0
SuccessExitStatus=0

[Install]
WantedBy=sysinit.target
`

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

	hostname := r.env.TemplateId

	hosts := fmt.Sprintf(`127.0.0.1   localhost
127.0.1.1   %s
`, hostname)

	e2bFile := fmt.Sprintf(`ENV_ID=%s
BUILD_ID=%s
`, r.env.TemplateId, r.env.BuildId)

	envdFileData, err := os.ReadFile(storage.HostEnvdPath)
	if err != nil {
		return fmt.Errorf("error reading envd file: %w", err)
	}

	// Delete files from underlying layers
	filesRemoveLayer, err := LayerFile(
		map[string]layerFile{
			"etc/.wh.hostname":    {[]byte{}, 0o644},
			"etc/.wh.hosts":       {[]byte{}, 0o644},
			"etc/.wh.resolv.conf": {[]byte{}, 0o644},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating layer from files: %w", err)
	}

	filesLayer, err := LayerFile(
		map[string]layerFile{
			// Setup system
			"etc/hostname":    {[]byte(hostname), 0o644},
			"etc/hosts":       {[]byte(hosts), 0o644},
			"etc/resolv.conf": {[]byte("nameserver 8.8.8.8"), 0o644},

			".e2b":                                 {[]byte(e2bFile), 0o644},
			storage.GuestEnvdPath:                  {envdFileData, 0o777},
			"provision.sh":                         {scriptDef.Bytes(), 0o777},
			"etc/systemd/system/provision.service": {[]byte(provisionService), 0o644},
			"etc/systemd/system/envd.service":      {[]byte(envdService), 0o644},
			"etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf": {[]byte(autologinService), 0o644},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating layer from files: %w", err)
	}

	symlinkLayer, err := LayerSymlink(
		map[string]string{
			// Enable provision service autostart
			"etc/systemd/system/sysinit.target.wants/provision.service": "etc/systemd/system/provision.service",
			// Enable envd service autostart
			"etc/systemd/system/multi-user.target.wants/envd.service": "etc/systemd/system/envd.service",
		},
	)
	if err != nil {
		return fmt.Errorf("error creating layer from symlinks: %w", err)
	}

	/*systemdLayer, err := createLayerFromFolder("/extract")
	if err != nil {
		return err
	}*/

	img, err = mutate.AppendLayers(img, filesRemoveLayer, filesLayer, symlinkLayer)
	if err != nil {
		return fmt.Errorf("error appending layers: %w", err)
	}

	telemetry.ReportEvent(childCtx, "set up filesystem")

	// Step 2: Flatten layers to tar stream
	pr := mutate.Extract(img)
	defer pr.Close()

	postProcessor.WriteMsg("Creating file system")
	rootfsFile, err := os.Create(rootfsPath)
	if err != nil {
		return fmt.Errorf("error creating rootfs file: %w", err)
	}
	defer func() {
		rootfsErr := rootfsFile.Close()
		if rootfsErr != nil {
			telemetry.ReportError(childCtx, fmt.Errorf("error closing rootfs file: %w", rootfsErr))
		} else {
			telemetry.ReportEvent(childCtx, "closed rootfs file")
		}
	}()
	// Step 3: Convert tar to ext4 image
	if err := tar2ext4.Convert(pr, rootfsFile, tar2ext4.ConvertWhiteout, tar2ext4.MaximumDiskSize(maxRootfsSize)); err != nil {
		if strings.Contains(err.Error(), "disk exceeded maximum size") {
			r.template.BuildLogsWriter.Write([]byte(fmt.Sprintf("Build failed - exceeded maximum size %v MB.\n", maxRootfsSize>>ToMBShift)))
		}

		errMsg := fmt.Errorf("error converting tar to ext4: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}
	telemetry.ReportEvent(childCtx, "created rootfs file")

	postProcessor.WriteMsg("Filesystem cleanup")
	telemetry.ReportEvent(childCtx, "converted container tar to ext4")

	tuneContext, tuneSpan := tracer.Start(childCtx, "tune-rootfs-file-cmd")
	defer tuneSpan.End()

	cmd := exec.CommandContext(tuneContext, "tune2fs", "-O ^read-only", rootfsPath)

	tuneStdoutWriter := telemetry.NewEventWriter(tuneContext, "stdout")
	cmd.Stdout = tuneStdoutWriter

	tuneStderrWriter := telemetry.NewEventWriter(childCtx, "stderr")
	cmd.Stderr = tuneStderrWriter

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("error making rootfs file writable: %w", err)
	}

	telemetry.ReportEvent(childCtx, "made rootfs file writable")

	rootfsStats, err := rootfsFile.Stat()
	if err != nil {
		return fmt.Errorf("error statting rootfs file: %w", err)
	}

	telemetry.ReportEvent(childCtx, fmt.Sprintf("statted rootfs file (size: %d)", rootfsStats.Size()))

	// In bytes
	rootfsSize := rootfsStats.Size() + r.template.DiskSizeMB<<ToMBShift

	r.template.rootfsSize = rootfsSize

	err = rootfsFile.Truncate(rootfsSize)
	if err != nil {
		return fmt.Errorf("error truncating rootfs file: %w to size of build + defaultDiskSizeMB", err)
	}

	telemetry.ReportEvent(childCtx, "truncated rootfs file to size of build + defaultDiskSizeMB")

	resizeContext, resizeSpan := tracer.Start(childCtx, "resize-rootfs-file-cmd")
	defer resizeSpan.End()

	cmd = exec.CommandContext(resizeContext, "resize2fs", rootfsPath)

	resizeStdoutWriter := telemetry.NewEventWriter(resizeContext, "stdout")
	cmd.Stdout = resizeStdoutWriter

	resizeStderrWriter := telemetry.NewEventWriter(resizeContext, "stderr")
	cmd.Stderr = resizeStderrWriter

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("error resizing rootfs file: %w", err)
	}

	telemetry.ReportEvent(childCtx, "resized rootfs file")

	// Check the rootfs filesystem corruption
	ext4Check, err := checkFileSystemExt4(rootfsFile.Name())
	zap.L().Debug("filesystem ext4 integrity",
		zap.String("result", ext4Check),
		zap.Error(err),
	)
	if err != nil {
		return fmt.Errorf("error checking ext4 filesystem integrity: %w", err)
	}

	return nil
}

func checkFileSystemExt4(rootfsPath string) (string, error) {
	cmd := exec.Command("e2fsck", "-n", rootfsPath)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Error(), fmt.Errorf("error running e2fsck: %w", err)
		} else {
			return string(out), fmt.Errorf("error running e2fsck: %w", err)
		}
	}
	return strings.TrimSpace(string(out)), nil
}
