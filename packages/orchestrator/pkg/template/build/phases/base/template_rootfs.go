package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/nbdutil"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/units"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// buildLayerFromTemplate materializes a source template's rootfs into a new ext4 file,
// resizes it to the target disk size, re-provisions it, and creates a new base layer.
func (bb *BaseBuilder) buildLayerFromTemplate(
	ctx context.Context,
	userLogger logger.Logger,
	baseMetadata metadata.Template,
	hash string,
) (metadata.Template, error) {
	ctx, span := tracer.Start(ctx, "build-layer-from-template", trace.WithAttributes(
		attribute.String("from_template", bb.Config.FromTemplate.GetBuildID()),
	))
	defer span.End()

	templateBuildDir := filepath.Join(bb.BuilderConfig.TemplatesDir, baseMetadata.Template.BuildID)
	err := os.MkdirAll(templateBuildDir, 0o777)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error creating template build directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(templateBuildDir); removeErr != nil {
			bb.logger.Error(ctx, "Error while removing template build directory", zap.Error(removeErr))
		}
	}()

	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	// Step 1: Materialize the source template rootfs to a local ext4 file
	userLogger.Info(ctx, "Materializing source template rootfs")
	err = bb.materializeTemplateRootfs(ctx, userLogger, rootfsPath)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error materializing template rootfs: %w", err)
	}

	// Step 2: Shrink, check integrity, and make writable
	_, err = filesystem.Shrink(ctx, rootfsPath)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error shrinking rootfs: %w", err)
	}

	ext4Check, err := filesystem.CheckIntegrity(ctx, rootfsPath, true)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error checking rootfs integrity after shrink: %w: %s", err, ext4Check)
	}

	err = filesystem.MakeWritable(ctx, rootfsPath)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error making rootfs writable: %w", err)
	}

	// Step 3: Resize to target disk size
	rootfsFreeSpace, err := filesystem.GetFreeSpace(ctx, rootfsPath, bb.Config.RootfsBlockSize())
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error getting free space: %w", err)
	}
	diskAdd := units.MBToBytes(bb.Config.DiskSizeMB) - rootfsFreeSpace
	if diskAdd > 0 {
		_, err = filesystem.Enlarge(ctx, rootfsPath, diskAdd)
		if err != nil {
			return metadata.Template{}, fmt.Errorf("error enlarging rootfs: %w", err)
		}
	}

	ext4Check, err = filesystem.CheckIntegrity(ctx, rootfsPath, true)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error checking rootfs integrity after resize: %w: %s", err, ext4Check)
	}

	// Step 4: Create block devices
	buildIDParsed, err := uuid.Parse(baseMetadata.Template.BuildID)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfsDevice, err := block.NewLocal(rootfsPath, bb.Config.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error creating rootfs block device: %w", err)
	}

	memfile, err := block.NewEmpty(
		units.MBToBytes(bb.Config.MemoryMB),
		config.MemfilePageSize(bb.Config.HugePages),
		buildIDParsed,
	)
	if err != nil {
		err = errors.Join(err, rootfsDevice.Close())
		return metadata.Template{}, fmt.Errorf("error creating memfile: %w", err)
	}

	// Step 5: Create local template
	cachePaths, err := storage.Paths{BuildID: baseMetadata.Template.BuildID}.Cache(bb.BuildContext.BuilderConfig.StorageConfig)
	if err != nil {
		err = errors.Join(err, rootfsDevice.Close(), memfile.Close())
		return metadata.Template{}, fmt.Errorf("error creating template cache paths: %w", err)
	}
	localTemplate := sbxtemplate.NewLocalTemplate(cachePaths, rootfsDevice, memfile)
	defer localTemplate.Close(ctx)

	// Step 6: Propagate env vars from source template
	sourceMeta, err := metadata.FromBuildID(ctx, bb.templateStorage, bb.Config.FromTemplate.GetBuildID())
	if err != nil {
		// If we can't read source metadata, proceed with whatever we have
		bb.logger.Warn(ctx, "could not read source template metadata for env vars propagation", zap.Error(err))
	} else {
		baseMetadata.Context.EnvVars = sourceMeta.Context.EnvVars
	}

	// Step 7: Provision sandbox with systemd and other vital parts
	userLogger.Info(ctx, "Provisioning sandbox template from source template")

	baseSbxConfig := sandbox.NewConfig(sandbox.Config{
		Vcpu:      bb.Config.VCpuCount,
		RamMB:     bb.Config.MemoryMB,
		HugePages: bb.Config.HugePages,

		Envd: sandbox.EnvdMetadata{
			Version: bb.EnvdVersion,
		},

		FirecrackerConfig: fc.Config{
			KernelVersion:      bb.Config.KernelVersion,
			FirecrackerVersion: bb.Config.FirecrackerVersion,
		},
	})
	err = bb.provisionSandbox(
		ctx,
		userLogger,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			TemplateID:  bb.Config.TemplateID,
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
			TeamID:      bb.Config.TeamID,
			BuildID:     bb.Template.BuildID,
			SandboxType: sandbox.SandboxTypeBuild,
		},
		localTemplate,
		rootfsPath,
		provisionLogPrefix,
	)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error provisioning sandbox from template: %w", err)
	}

	// Step 8: Post-provision integrity check and enlarge
	ext4Check, err = filesystem.CheckIntegrity(ctx, rootfsPath, true)
	if err != nil {
		logger.L().Error(ctx, "provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		return metadata.Template{}, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
	}

	err = bb.enlargeDiskAfterProvisioning(ctx, bb.Config, rootfsDevice)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error enlarging disk after provisioning: %w", err)
	}

	// Step 9: Build final layer (create sandbox with systemd, pause, snapshot, upload)
	userLogger.Debug(ctx, "Creating base sandbox template layer from source template")

	sandboxOptions := []layer.CreateSandboxOption{
		layer.WithRootfsCachePath(rootfsPath),
	}
	sandboxOptions = append(sandboxOptions, layer.ReservedBlocksOptions(ctx, bb.featureFlags, bb.Config.RootfsBlockSize())...)

	sandboxCreator := layer.NewCreateSandbox(
		baseSbxConfig,
		bb.sandboxFactory,
		baseLayerTimeout,
		sandboxOptions...,
	)

	actionExecutor := layer.NewFunctionAction(func(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (metadata.Template, error) {
		err = sandboxtools.SyncChangesToDisk(
			ctx,
			bb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return metadata.Template{}, fmt.Errorf("error running sync command: %w", err)
		}

		return meta, nil
	})

	templateProvider := layer.NewDirectSourceTemplateProvider(localTemplate)

	baseLayer, err := bb.layerExecutor.BuildLayer(
		ctx,
		userLogger,
		layer.LayerBuildCommand{
			SourceTemplate: templateProvider,
			CurrentLayer:   baseMetadata,
			Hash:           hash,
			UpdateEnvd:     false,
			SandboxCreator: sandboxCreator,
			ActionExecutor: actionExecutor,
		},
	)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error building base layer from template: %w", err)
	}

	return baseLayer, nil
}

// materializeTemplateRootfs loads the source template's rootfs via NBD, creates a new ext4,
// copies all files from source to destination, and overwrites with fresh provisioning files.
func (bb *BaseBuilder) materializeTemplateRootfs(
	ctx context.Context,
	userLogger logger.Logger,
	rootfsPath string,
) error {
	ctx, span := tracer.Start(ctx, "materialize-template-rootfs")
	defer span.End()

	// We use a separate context for NBD operations to avoid cleanup deadlocks on cancellation
	nbdCtx := context.Background()

	// Load source template's rootfs from storage
	sourceDevice, sourceCleaner, err := nbdutil.TemplateRootfs(ctx, bb.Config.FromTemplate.GetBuildID())
	if err != nil {
		return fmt.Errorf("error loading source template rootfs: %w", err)
	}
	defer sourceCleaner.Run(ctx, 30*time.Second)

	// Create COW cache on the source rootfs
	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.ext4.cow.cache-%s",
		bb.Config.FromTemplate.GetBuildID(), uuid.New().String()))
	defer os.RemoveAll(cowCachePath)

	cache, err := block.NewCache(
		int64(sourceDevice.Header().Metadata.Size),
		int64(sourceDevice.Header().Metadata.BlockSize),
		cowCachePath,
		false,
	)
	if err != nil {
		return fmt.Errorf("error creating COW cache: %w", err)
	}

	overlay := block.NewOverlay(sourceDevice, cache)
	defer overlay.Close()

	// Expose source rootfs via NBD
	devicePath, deviceCleaner, err := nbdutil.GetNBDDevice(nbdCtx, overlay, bb.featureFlags)
	if err != nil {
		return fmt.Errorf("error exposing source rootfs via NBD: %w", err)
	}
	defer deviceCleaner.Run(ctx, 30*time.Second)

	// Mount source rootfs read-only
	srcMountPath, err := os.MkdirTemp("", "template-src-mount-")
	if err != nil {
		return fmt.Errorf("error creating source mount directory: %w", err)
	}
	defer os.RemoveAll(srcMountPath)

	err = unix.Mount(string(devicePath), srcMountPath, "ext4", unix.MS_RDONLY, "")
	if err != nil {
		return fmt.Errorf("error mounting source rootfs: %w", err)
	}
	defer func() {
		if umountErr := unix.Unmount(srcMountPath, 0); umountErr != nil {
			logger.L().Error(ctx, "error unmounting source rootfs", zap.Error(umountErr))
		}
	}()

	userLogger.Debug(ctx, "Source template rootfs mounted")

	// Create new ext4 file at max build size
	maxSizeMB := int64(bb.featureFlags.IntFlag(ctx, featureflags.BuildBaseRootfsSizeLimitMB))
	err = filesystem.Make(ctx, rootfsPath, maxSizeMB, bb.Config.RootfsBlockSize())
	if err != nil {
		return fmt.Errorf("error creating new ext4 filesystem: %w", err)
	}

	// Mount new ext4
	destMountPath, err := os.MkdirTemp("", "template-dest-mount-")
	if err != nil {
		return fmt.Errorf("error creating destination mount directory: %w", err)
	}
	defer os.RemoveAll(destMountPath)

	err = filesystem.Mount(ctx, rootfsPath, destMountPath)
	if err != nil {
		return fmt.Errorf("error mounting new ext4: %w", err)
	}
	defer func() {
		if umountErr := filesystem.Unmount(context.WithoutCancel(ctx), destMountPath); umountErr != nil {
			logger.L().Error(ctx, "error unmounting destination rootfs", zap.Error(umountErr))
		}
	}()

	// Copy all files from source to destination via rsync
	userLogger.Info(ctx, "Copying files from source template")
	err = copyFilesRsync(ctx, srcMountPath, destMountPath)
	if err != nil {
		return fmt.Errorf("error copying files from source template: %w", err)
	}

	// Overwrite with fresh provisioning files
	userLogger.Debug(ctx, "Writing fresh provisioning files")
	err = bb.writeProvisioningFiles(ctx, destMountPath)
	if err != nil {
		return fmt.Errorf("error writing provisioning files: %w", err)
	}

	return nil
}

// copyFilesRsync copies all files from src to dest using rsync, preserving
// permissions, ownership, timestamps, symlinks, and hard links.
func copyFilesRsync(ctx context.Context, src, dest string) error {
	cmd := exec.CommandContext(ctx, "rsync", "-aH", "--whole-file", "--inplace", src+"/", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync from %s to %s: %w: %s", src, dest, err, string(out))
	}

	return nil
}

// writeProvisioningFiles writes the latest provisioning files (envd, busybox,
// provision script, system config templates, symlinks) to the mounted rootfs.
func (bb *BaseBuilder) writeProvisioningFiles(ctx context.Context, destMountPath string) error {
	provisionScript, err := getProvisionScript(ctx, ProvisionScriptParams{
		BusyBox:    rootfs.SandboxBusyBoxPath,
		ResultPath: provisionScriptResultPath,
		Provider:   bb.BuildContext.BuilderConfig.Provider,
	})
	if err != nil {
		return fmt.Errorf("error generating provision script: %w", err)
	}

	files, symlinks, err := rootfs.ProvisioningFiles(
		bb.BuildContext,
		provisionScript,
		provisionLogPrefix,
		provisionScriptResultPath,
	)
	if err != nil {
		return fmt.Errorf("error getting provisioning files: %w", err)
	}

	// Write files
	for path, f := range files {
		// Normalize path: strip leading slash to make it relative
		relPath := strings.TrimPrefix(path, "/")
		fullPath := filepath.Join(destMountPath, relPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("error creating directory for %s: %w", relPath, err)
		}

		if err := os.WriteFile(fullPath, f.Bytes, os.FileMode(f.Mode)); err != nil {
			return fmt.Errorf("error writing file %s: %w", relPath, err)
		}
	}

	// Create symlinks
	for linkPath, targetPath := range symlinks {
		relLinkPath := strings.TrimPrefix(linkPath, "/")
		fullLinkPath := filepath.Join(destMountPath, relLinkPath)

		// Target is relative to rootfs root, so prepend "/" to make it absolute inside the guest
		relTargetPath := strings.TrimPrefix(targetPath, "/")
		absTarget := "/" + relTargetPath

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(fullLinkPath), 0o755); err != nil {
			return fmt.Errorf("error creating directory for symlink %s: %w", relLinkPath, err)
		}

		// Remove existing file/symlink if present
		os.Remove(fullLinkPath)

		if err := os.Symlink(absTarget, fullLinkPath); err != nil {
			return fmt.Errorf("error creating symlink %s -> %s: %w", relLinkPath, absTarget, err)
		}
	}

	return nil
}
