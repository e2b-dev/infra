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
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const layerTimeout = time.Hour

func (b *Builder) buildLayer(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	uploadErrGroup *errgroup.Group,
	sourceSbxConfig *orchestrator.SandboxConfig,
	finalTemplateID string,
	baseHash,
	hash string,
	sourceTemplate config.TemplateMetadata,
	exportTemplate config.TemplateMetadata,
	resumeSandbox bool,
	allowInternet bool,
	fun func(ctx context.Context, sbx *sandbox.Sandbox) error,
) error {
	ctx, childSpan := b.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	postProcessor.WriteMsg(fmt.Sprintf("Running action in: %s/%s", sourceTemplate.TemplateID, sourceTemplate.BuildID))

	// Resume sandbox source files/config
	sbxConfig := &orchestrator.SandboxConfig{
		TemplateId:         sourceTemplate.TemplateID,
		BuildId:            sourceTemplate.BuildID,
		ExecutionId:        uuid.NewString(),
		KernelVersion:      sourceSbxConfig.KernelVersion,
		FirecrackerVersion: sourceSbxConfig.FirecrackerVersion,
		HugePages:          sourceSbxConfig.HugePages,
		SandboxId:          sourceSbxConfig.SandboxId,
		EnvdVersion:        sourceSbxConfig.EnvdVersion,
		Vcpu:               sourceSbxConfig.Vcpu,
		RamMb:              sourceSbxConfig.RamMb,

		BaseTemplateId: sourceSbxConfig.BaseTemplateId,
	}

	var sbx *sandbox.Sandbox
	var cleanupRes *sandbox.Cleanup
	var err error
	if resumeSandbox {
		postProcessor.WriteMsg("Resuming sandbox")
		sbx, cleanupRes, err = sandbox.ResumeSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			b.templateCache,
			sbxConfig,
			uuid.New().String(),
			time.Now(),
			time.Now().Add(time.Minute),
			b.devicePool,
			allowInternet,
			false,
		)
	} else {
		postProcessor.WriteMsg("Creating new sandbox")
		var localTemplate sbxtemplate.Template

		// Sandbox source files
		localTemplate, err = b.templateCache.GetTemplate(
			sbxConfig.TemplateId,
			sbxConfig.BuildId,
			sbxConfig.KernelVersion,
			sbxConfig.FirecrackerVersion,
		)
		if err != nil {
			return fmt.Errorf("failed to get template snapshot data: %w", err)
		}

		var oldMemfile block.ReadonlyDevice
		oldMemfile, err = localTemplate.Memfile()
		if err != nil {
			return fmt.Errorf("error getting memfile from local template: %w", err)
		}

		// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
		// This is ok as the sandbox is started from the beginning.
		var memfile block.ReadonlyDevice
		memfile, err = block.NewEmpty(sbxConfig.RamMb<<constants.ToMBShift, oldMemfile.BlockSize(), uuid.MustParse(sbxConfig.BuildId))
		if err != nil {
			return fmt.Errorf("error creating memfile: %w", err)
		}
		err = localTemplate.ReplaceMemfile(memfile)
		if err != nil {
			return fmt.Errorf("error setting memfile for local template: %w", err)
		}

		// New sandbox config
		sbxConfig.TemplateId = exportTemplate.TemplateID
		sbxConfig.BuildId = exportTemplate.BuildID
		sbxConfig.BaseTemplateId = exportTemplate.TemplateID
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

	err = fun(ctx, sbx)
	if err != nil {
		return fmt.Errorf("error running action in sandbox: %w", err)
	}

	err = pauseAndUpload(
		ctx,
		b.tracer,
		uploadErrGroup,
		postProcessor,
		b.storage,
		b.templateCache,
		sbx,
		finalTemplateID,
		baseHash,
		hash,
		exportTemplate.TemplateID,
		exportTemplate.BuildID,
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
	persistance storage.StorageProvider,
	templateCache *sbxtemplate.Cache,
	sbx *sandbox.Sandbox,
	finalTemplateID string,
	baseHash,
	hash string,
	templateID string,
	buildID string,
) error {
	ctx, childSpan := tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	// Pause sandbox
	snapshotTemplateFiles := storage.NewTemplateFiles(
		templateID,
		buildID,
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
	)
	postProcessor.WriteMsg(fmt.Sprintf("Caching template layer: %s/%s", snapshotTemplateFiles.TemplateId, buildID))

	snapshotTemplateCacheFiles, err := snapshotTemplateFiles.NewTemplateCacheFiles()
	if err != nil {
		return fmt.Errorf("error creating template files: %w", err)
	}
	// snapshot is automatically cleared by the templateCache eviction
	snapshot, err := sbx.Pause(
		ctx,
		tracer,
		snapshotTemplateCacheFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	// Add snapshot to template cache so it can be used immediately
	err = templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateId,
		snapshotTemplateFiles.BuildId,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
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
		postProcessor.WriteMsg(fmt.Sprintf("Uploading template layer: %s/%s", snapshotTemplateFiles.TemplateId, snapshotTemplateFiles.BuildId))
		err := snapshot.Upload(
			ctx,
			persistance,
			snapshotTemplateFiles,
		)
		if err != nil {
			return fmt.Errorf("error uploading snapshot: %w", err)
		}

		err = saveTemplateToHash(ctx, persistance, finalTemplateID, baseHash, hash, config.TemplateMetadata{
			TemplateID: snapshotTemplateFiles.TemplateId,
			BuildID:    snapshotTemplateFiles.BuildId,
		})
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		postProcessor.WriteMsg(fmt.Sprintf("Template layer saved: %s/%s", snapshotTemplateFiles.TemplateId, snapshotTemplateFiles.BuildId))
		return nil
	})

	return nil
}
