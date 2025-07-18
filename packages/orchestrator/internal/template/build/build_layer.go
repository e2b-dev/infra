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
	finalTemplateID string,
	hash string,
	sourceMeta storage.TemplateFiles,
	exportMeta storage.TemplateFiles,
	resumeSandbox bool,
	allowInternet bool,
	fn func(ctx context.Context, sbx *sandbox.Sandbox) error,
) error {
	ctx, childSpan := b.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	var sbx *sandbox.Sandbox
	var cleanupRes *sandbox.Cleanup
	var err error

	localTemplate, err := b.templateCache.GetTemplate(
		sourceMeta.TemplateID,
		sourceMeta.BuildID,
		sourceMeta.KernelVersion,
		sourceMeta.FirecrackerVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	if resumeSandbox {
		sbxConfig := &orchestrator.SandboxConfig{
			SandboxId:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionId: uuid.NewString(),

			TemplateId:         sourceMeta.TemplateID,
			BuildId:            sourceMeta.BuildID,
			KernelVersion:      sourceMeta.KernelVersion,
			FirecrackerVersion: sourceMeta.FirecrackerVersion,

			BaseTemplateId: sourceSbxConfig.BaseTemplateId,

			HugePages:   sourceSbxConfig.HugePages,
			EnvdVersion: sourceSbxConfig.EnvdVersion,
			Vcpu:        sourceSbxConfig.Vcpu,
			RamMb:       sourceSbxConfig.RamMb,
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
			allowInternet,
			false,
		)
	} else {
		var oldMemfile block.ReadonlyDevice
		oldMemfile, err = localTemplate.Memfile()
		if err != nil {
			return fmt.Errorf("error getting memfile from local template: %w", err)
		}

		// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
		// This is ok as the sandbox is started from the beginning.
		var memfile block.ReadonlyDevice
		memfile, err = block.NewEmpty(
			sourceSbxConfig.RamMb<<constants.ToMBShift,
			oldMemfile.BlockSize(),
			uuid.MustParse(sourceMeta.BuildID),
		)
		if err != nil {
			return fmt.Errorf("error creating memfile: %w", err)
		}
		err = localTemplate.ReplaceMemfile(memfile)
		if err != nil {
			return fmt.Errorf("error setting memfile for local template: %w", err)
		}

		sbxConfig := &orchestrator.SandboxConfig{
			SandboxId:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionId: uuid.NewString(),

			TemplateId:         exportMeta.TemplateID,
			BuildId:            exportMeta.BuildID,
			KernelVersion:      exportMeta.KernelVersion,
			FirecrackerVersion: exportMeta.FirecrackerVersion,

			BaseTemplateId: exportMeta.TemplateID,

			HugePages:   sourceSbxConfig.HugePages,
			EnvdVersion: sourceSbxConfig.EnvdVersion,
			Vcpu:        sourceSbxConfig.Vcpu,
			RamMb:       sourceSbxConfig.RamMb,
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
			allowInternet,
		)
	}
	defer func() {
		cleanupErr := cleanupRes.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}
	}()
	if err != nil {
		return fmt.Errorf("error resuming sandbox: %w", err)
	}
	if !resumeSandbox {
		err = sbx.WaitForEnvd(
			ctx,
			b.tracer,
			waitEnvdTimeout,
		)
		if err != nil {
			return fmt.Errorf("failed to wait for sandbox start: %w", err)
		}
	}

	// Add to proxy so we can call envd commands
	b.sandboxes.Insert(sbx.Metadata.Config.SandboxId, sbx)
	defer func() {
		b.sandboxes.Remove(sbx.Metadata.Config.SandboxId)
		b.proxy.RemoveFromPool(sbx.Metadata.Config.ExecutionId)
	}()

	err = fn(ctx, sbx)
	if err != nil {
		return fmt.Errorf("error running action in sandbox: %w", err)
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
		finalTemplateID,
		hash,
		exportMeta,
	)
	if err != nil {
		return fmt.Errorf("error pausing and uploading template: %w", err)
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
	finalTemplateID string,
	hash string,
	template storage.TemplateFiles,
) error {
	ctx, childSpan := tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	postProcessor.WriteMsg(fmt.Sprintf("Saving layer: %s/%s", template.TemplateID, template.BuildID))

	cacheFiles, err := template.CacheFiles()
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
	err = templateCache.AddSnapshot(
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

		err = saveTemplateMeta(ctx, buildStorage, finalTemplateID, hash, cacheFiles.TemplateFiles)
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		postProcessor.WriteMsg(fmt.Sprintf("Saved: %s/%s", cacheFiles.TemplateID, cacheFiles.BuildID))
		return nil
	})

	return nil
}
