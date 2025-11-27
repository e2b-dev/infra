package rootfs

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"text/template"

	"github.com/dustin/go-humanize"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/systeminit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs")

//go:embed files
var files embed.FS
var fileTemplates = template.Must(template.ParseFS(files, "files/*"))

const (
	// Max size of the rootfs file in MB.
	maxRootfsSize = 25000 << constants.ToMBShift

	BusyBoxPath     = "usr/bin/busybox"
	BusyBoxInitPath = "usr/bin/init"

	ProvisioningExitPrefix = "E2B_PROVISIONING_EXIT:"
)

type Rootfs struct {
	buildContext        buildcontext.BuildContext
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
	buildContext buildcontext.BuildContext,
) *Rootfs {
	return &Rootfs{
		buildContext:        buildContext,
		artifactRegistry:    artifactRegistry,
		dockerhubRepository: dockerhubRepository,
	}
}

func (r *Rootfs) CreateExt4Filesystem(
	ctx context.Context,
	l logger.Logger,
	rootfsPath string,
	provisionScript string,
	provisionLogPrefix string,
	provisionResultPath string,
) (c containerregistry.Config, e error) {
	template := r.buildContext.Config

	childCtx, childSpan := tracer.Start(ctx, "create-ext4-file")
	defer childSpan.End()

	defer func() {
		if e != nil {
			telemetry.ReportCriticalError(childCtx, "failed to create ext4 filesystem", e)
		}
	}()

	l.Debug(ctx, "Requesting Docker Image")

	var img containerregistry.Image
	var err error
	if template.FromImage != "" {
		img, err = oci.GetPublicImage(childCtx, r.dockerhubRepository, template.FromImage, template.RegistryAuthProvider)
	} else {
		img, err = oci.GetImage(childCtx, r.artifactRegistry, template.TemplateID, r.buildContext.Template.BuildID)
	}
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error requesting docker image: %w", err)
	}

	imageSize, err := oci.GetImageSize(img)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting image size: %w", err)
	}
	l.Info(ctx, fmt.Sprintf("Base Docker image size: %s", humanize.Bytes(uint64(imageSize))))

	l.Debug(ctx, "Setting up system files")
	layers, err := additionalOCILayers(r.buildContext, provisionScript, provisionLogPrefix, provisionResultPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error populating filesystem: %w", err)
	}
	img, err = mutate.AppendLayers(img, layers...)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error appending layers: %w", err)
	}
	telemetry.ReportEvent(childCtx, "set up filesystem")

	l.Info(ctx, "Creating file system and pulling Docker image")
	ext4Size, err := oci.ToExt4(ctx, l, img, rootfsPath, maxRootfsSize, template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error converting oci to ext4: %w", err)
	}
	telemetry.ReportEvent(childCtx, "created rootfs ext4 file")

	l.Debug(ctx, "Filesystem cleanup")
	// Make rootfs writable, be default it's readonly
	err = filesystem.MakeWritable(ctx, rootfsPath)
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error making rootfs file writable: %w", err)
	}

	// Resize rootfs
	rootfsFreeSpace, err := filesystem.GetFreeSpace(ctx, rootfsPath, template.RootfsBlockSize())
	if err != nil {
		return containerregistry.Config{}, fmt.Errorf("error getting free space: %w", err)
	}
	// We need to remove the remaining free space from the ext4 file size
	// This is a residual space that could not be shrunk when creating the filesystem,
	// but is still available for use
	diskAdd := template.DiskSizeMB<<constants.ToMBShift - rootfsFreeSpace
	logger.L().Debug(ctx, "adding disk size diff to rootfs",
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
	logger.L().Debug(ctx, "filesystem ext4 integrity",
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
	buildContext buildcontext.BuildContext,
	provisionScript string,
	provisionLogPrefix string,
	provisionResultPath string,
) ([]containerregistry.Layer, error) {
	envdFileData, err := os.ReadFile(buildContext.BuilderConfig.HostEnvdPath)
	if err != nil {
		return nil, fmt.Errorf("error reading envd file: %w", err)
	}

	filesMap := map[string]oci.File{
		storage.GuestEnvdPath: {Bytes: envdFileData, Mode: 0o777},

		// Provision script
		"usr/local/bin/provision.sh": {Bytes: []byte(provisionScript), Mode: 0o777},
		// Setup init system
		BusyBoxPath: {Bytes: systeminit.BusyboxBinary, Mode: 0o755},
		// Set to bin/init so it's not in conflict with systemd
		// Any rewrite of the init file when booted from it will corrupt the filesystem
		BusyBoxInitPath: {Bytes: systeminit.BusyboxBinary, Mode: 0o755},
	}

	// add templates
	for _, t := range fileTemplates.Templates() {
		var buff bytes.Buffer
		model := newTemplateModel(buildContext, provisionLogPrefix, provisionResultPath)
		if err := t.Execute(&buff, &model); err != nil {
			return nil, fmt.Errorf("error executing template %q: %w", t.Name(), err)
		}
		if len(model.paths) == 0 {
			return nil, fmt.Errorf("template %q did not set path", t.Name())
		}

		data := bytes.TrimSpace(buff.Bytes())
		for _, path := range model.paths {
			filesMap[path.path] = oci.File{
				Bytes: data,
				Mode:  path.mode,
			}
		}
	}

	filesLayer, err := oci.LayerFile(filesMap)
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

type templateModel struct {
	Context buildcontext.BuildContext

	ProvisionLogPrefix  string
	ProvisionResultPath string
	ProvisionExitPrefix string

	paths []struct {
		path string
		mode int64
	}
}

func newTemplateModel(buildContext buildcontext.BuildContext, provisionLogPrefix, provisionResultPath string) *templateModel {
	return &templateModel{
		Context:             buildContext,
		ProvisionLogPrefix:  provisionLogPrefix,
		ProvisionExitPrefix: ProvisioningExitPrefix,
		ProvisionResultPath: provisionResultPath,
	}
}

func (t *templateModel) WriteFile(path string, mode int64) string {
	t.paths = append(t.paths, struct {
		path string
		mode int64
	}{
		path: strings.TrimPrefix(path, "/"),
		mode: mode,
	})

	return "" // no real return value
}

func (t *templateModel) MemoryLimit() int {
	return int(math.Min(float64(t.Context.Config.MemoryMB)/2, 512))
}
