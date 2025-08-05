package build

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const layerTimeout = time.Hour

func (b *Builder) buildLayer(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	uploadErrGroup *errgroup.Group,
	sourceSbxConfig *orchestrator.SandboxConfig,
	cacheScope string,
	hash string,
	sourceMeta LayerMetadata,
	exportTemplate storage.TemplateFiles,
	resumeSandbox bool,
	updateEnvd bool,
	fn func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error),
) (LayerMetadata, error) {
	ctx, childSpan := b.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	var sbx *sandbox.Sandbox
	var cleanupRes *sandbox.Cleanup
	var err error

	localTemplate, err := b.templateCache.GetTemplate(ctx,
		sourceMeta.Template.TemplateID,
		sourceMeta.Template.BuildID,
		sourceMeta.Template.KernelVersion,
		sourceMeta.Template.FirecrackerVersion,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	if resumeSandbox {
		sbxConfig := &orchestrator.SandboxConfig{
			SandboxId:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionId: uuid.NewString(),

			TemplateId:         sourceMeta.Template.TemplateID,
			BuildId:            sourceMeta.Template.BuildID,
			KernelVersion:      sourceMeta.Template.KernelVersion,
			FirecrackerVersion: sourceMeta.Template.FirecrackerVersion,

			BaseTemplateId: sourceSbxConfig.BaseTemplateId,

			HugePages:   sourceSbxConfig.HugePages,
			EnvdVersion: sourceSbxConfig.EnvdVersion,
			Vcpu:        sourceSbxConfig.Vcpu,
			RamMb:       sourceSbxConfig.RamMb,

			AllowInternetAccess: sourceSbxConfig.AllowInternetAccess,
		}
		sbx, cleanupRes, err = sandbox.ResumeSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			localTemplate,
			sbxConfig,
			uuid.New().String(),
			time.Now(),
			time.Now().Add(layerTimeout),
			b.devicePool,
			false,
		)
	} else {
		var oldMemfile block.ReadonlyDevice
		oldMemfile, err = localTemplate.Memfile()
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("error getting memfile from local template: %w", err)
		}

		// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
		// This is ok as the sandbox is started from the beginning.
		var memfile block.ReadonlyDevice
		memfile, err = block.NewEmpty(
			sourceSbxConfig.RamMb<<constants.ToMBShift,
			oldMemfile.BlockSize(),
			uuid.MustParse(sourceMeta.Template.BuildID),
		)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("error creating memfile: %w", err)
		}
		err = localTemplate.ReplaceMemfile(memfile)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("error setting memfile for local template: %w", err)
		}

		sbxConfig := &orchestrator.SandboxConfig{
			SandboxId:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionId: uuid.NewString(),

			TemplateId:         exportTemplate.TemplateID,
			BuildId:            exportTemplate.BuildID,
			KernelVersion:      exportTemplate.KernelVersion,
			FirecrackerVersion: exportTemplate.FirecrackerVersion,

			BaseTemplateId: exportTemplate.TemplateID,

			HugePages:   sourceSbxConfig.HugePages,
			EnvdVersion: sourceSbxConfig.EnvdVersion,
			Vcpu:        sourceSbxConfig.Vcpu,
			RamMb:       sourceSbxConfig.RamMb,

			AllowInternetAccess: sourceSbxConfig.AllowInternetAccess,
		}
		sbx, cleanupRes, err = sandbox.CreateSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			b.devicePool,
			sbxConfig,
			localTemplate,
			layerTimeout,
			"",
			fc.ProcessOptions{
				InitScriptPath:      systemdInitPath,
				KernelLogs:          env.IsDevelopment(),
				SystemdToKernelLogs: false,
			},
		)
	}
	defer func() {
		cleanupErr := cleanupRes.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}
	}()
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error resuming sandbox: %w", err)
	}
	if !resumeSandbox {
		err = sbx.WaitForEnvd(
			ctx,
			b.tracer,
			waitEnvdTimeout,
		)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("failed to wait for sandbox start: %w", err)
		}
	}

	// Add to proxy so we can call envd commands
	b.sandboxes.Insert(sbx.Metadata.Config.SandboxId, sbx)
	defer func() {
		b.sandboxes.Remove(sbx.Metadata.Config.SandboxId)
		b.proxy.RemoveFromPool(sbx.Metadata.Config.ExecutionId)
	}()

	// Update envd binary to the latest version
	if updateEnvd {
		err = b.updateEnvdInSandbox(ctx, b.tracer, postProcessor, sbx)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("failed to update envd in sandbox: %w", err)
		}
	}

	meta, err := fn(ctx, sbx)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error running action in sandbox: %w", err)
	}

	exportMeta := LayerMetadata{
		Template: exportTemplate,
		Metadata: meta,
	}
	err = pauseAndUpload(
		ctx,
		b.tracer,
		uploadErrGroup,
		postProcessor,
		b.templateStorage,
		b.buildStorage,
		b.templateCache,
		sbx,
		cacheScope,
		hash,
		exportMeta,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error pausing and uploading template: %w", err)
	}

	return exportMeta, nil
}

// updateEnvdInSandbox updates the envd binary in the sandbox to the latest version.
func (b *Builder) updateEnvdInSandbox(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	sbx *sandbox.Sandbox,
) error {
	ctx, childSpan := tracer.Start(ctx, "update-envd")
	defer childSpan.End()

	envdVersion, err := envd.GetEnvdVersion(ctx)
	if err != nil {
		return fmt.Errorf("error getting envd version: %w", err)
	}
	postProcessor.Debug(fmt.Sprintf("Updating envd to version v%s", envdVersion))

	// Step 1: Copy the updated envd binary from host to /tmp in sandbox
	tmpEnvdPath := "/tmp/envd_updated"
	err = sandboxtools.CopyFile(
		ctx,
		b.tracer,
		b.proxy,
		sbx.Metadata.Config.SandboxId,
		"root",
		storage.HostEnvdPath,
		tmpEnvdPath,
	)
	if err != nil {
		return fmt.Errorf("failed to copy envd binary to sandbox: %w", err)
	}

	// Step 2: Replace the binary
	replaceEnvdCmd := fmt.Sprintf(`
		# Replace the binary and set permissions
		chmod +x %s
		mv -f %s %s
	`, tmpEnvdPath, tmpEnvdPath, storage.GuestEnvdPath)

	err = sandboxtools.RunCommandWithLogger(
		ctx,
		b.tracer,
		b.proxy,
		postProcessor,
		zap.DebugLevel,
		"update-envd-replace",
		sbx.Metadata.Config.SandboxId,
		replaceEnvdCmd,
		sandboxtools.CommandMetadata{User: "root"},
	)
	if err != nil {
		return fmt.Errorf("failed to replace envd binary: %w", err)
	}

	// Step 3: Restart the systemd envd service
	// Error is ignored because it's expected the envd connection will be lost
	_ = sandboxtools.RunCommand(
		ctx,
		b.tracer,
		b.proxy,
		sbx.Metadata.Config.SandboxId,
		"systemctl restart envd",
		sandboxtools.CommandMetadata{User: "root"},
	)

	// Step 4: Wait for envd to initialize
	err = sbx.WaitForEnvd(
		ctx,
		b.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return fmt.Errorf("failed to wait for envd initialization after update: %w", err)
	}

	return nil
}

func pauseAndUpload(
	ctx context.Context,
	tracer trace.Tracer,
	uploadErrGroup *errgroup.Group,
	postProcessor *writer.PostProcessor,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	templateCache *sbxtemplate.Cache,
	sbx *sandbox.Sandbox,
	cacheScope string,
	hash string,
	layerMeta LayerMetadata,
) error {
	ctx, childSpan := tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	postProcessor.Debug(fmt.Sprintf("Saving layer: %s/%s", layerMeta.Template.TemplateID, layerMeta.Template.BuildID))

	cacheFiles, err := layerMeta.Template.CacheFiles()
	if err != nil {
		return fmt.Errorf("error creating template files: %w", err)
	}
	// snapshot is automatically cleared by the templateCache eviction
	snapshot, err := sbx.Pause(
		ctx,
		tracer,
		cacheFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	// Add snapshot to template cache so it can be used immediately
	err = templateCache.AddSnapshot(ctx,
		cacheFiles.TemplateID,
		cacheFiles.BuildID,
		cacheFiles.KernelVersion,
		cacheFiles.FirecrackerVersion,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		return fmt.Errorf("error adding snapshot to template cache: %w", err)
	}

	// Upload snapshot async, it's added to the template cache immediately
	uploadErrGroup.Go(func() error {
		err := snapshot.Upload(
			ctx,
			templateStorage,
			cacheFiles.TemplateFiles,
		)
		if err != nil {
			return fmt.Errorf("error uploading snapshot: %w", err)
		}

		err = saveLayerMeta(ctx, buildStorage, cacheScope, hash, layerMeta)
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		postProcessor.Debug(fmt.Sprintf("Saved: %s/%s", cacheFiles.TemplateID, cacheFiles.BuildID))
		return nil
	})

	return nil
}
