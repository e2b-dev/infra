package layer

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer")

type LayerExecutor struct {
	buildcontext.BuildContext

	logger logger.Logger

	templateCache   *sbxtemplate.Cache
	proxy           *proxy.SandboxProxy
	sandboxes       *sandbox.Map
	templateStorage storage.StorageProvider
	buildStorage    storage.StorageProvider
	index           cache.Index
	uploadTracker   *UploadTracker
}

func NewLayerExecutor(
	buildContext buildcontext.BuildContext,
	logger logger.Logger,
	templateCache *sbxtemplate.Cache,
	proxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	index cache.Index,
	uploadTracker *UploadTracker,
) *LayerExecutor {
	return &LayerExecutor{
		BuildContext: buildContext,

		logger: logger,

		templateCache:   templateCache,
		proxy:           proxy,
		sandboxes:       sandboxes,
		templateStorage: templateStorage,
		buildStorage:    buildStorage,
		index:           index,
		uploadTracker:   uploadTracker,
	}
}

// BuildLayer orchestrates the layer building process
func (lb *LayerExecutor) BuildLayer(
	ctx context.Context,
	userLogger logger.Logger,
	cmd LayerBuildCommand,
) (metadata.Template, error) {
	ctx, childSpan := tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	localTemplate, err := cmd.SourceTemplate.Get(ctx, lb.templateCache)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("get template snapshot: %w", err)
	}

	// Create or resume sandbox
	sbx, err := cmd.SandboxCreator.Sandbox(ctx, lb, localTemplate)
	if err != nil {
		return metadata.Template{}, err
	}
	defer sbx.Close(ctx)

	// Add to proxy so we can call envd and route traffic from the sandbox
	lb.sandboxes.Insert(sbx)
	defer func() {
		lb.sandboxes.Remove(sbx.Runtime.SandboxID)

		closeErr := lb.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
		if closeErr != nil {
			// Errors here will be from forcefully closing the connections, so we can ignore them—they will at worst timeout on their own.
			lb.logger.Warn(ctx, "errors when manually closing connections to sandbox", zap.Error(closeErr))
		} else {
			lb.logger.Debug(
				ctx,
				"removed proxy from pool",
				logger.WithSandboxID(sbx.Runtime.SandboxID),
				logger.WithExecutionID(sbx.Runtime.ExecutionID),
			)
		}
	}()

	// Update envd binary to the latest version
	if cmd.UpdateEnvd {
		err = lb.updateEnvdInSandbox(ctx, userLogger, sbx)
		if err != nil {
			lb.logger.Error(
				ctx,
				"error updating envd",
				logger.WithSandboxID(sbx.Runtime.SandboxID),
				logger.WithExecutionID(sbx.Runtime.ExecutionID),
				zap.Error(err),
			)

			return metadata.Template{}, fmt.Errorf("update envd: %w", err)
		}
	}

	// Execute the action using the executor
	meta, err := cmd.ActionExecutor.Execute(ctx, sbx, cmd.CurrentLayer)
	if err != nil {
		lb.logger.Error(
			ctx,
			"error executing action",
			logger.WithSandboxID(sbx.Runtime.SandboxID),
			logger.WithExecutionID(sbx.Runtime.ExecutionID),
			zap.Error(err),
		)

		return metadata.Template{}, err
	}

	// Prepare metadata
	meta = meta.NewVersionTemplate(metadata.TemplateMetadata{
		BuildID:            cmd.CurrentLayer.Template.BuildID,
		KernelVersion:      sbx.Config.FirecrackerConfig.KernelVersion,
		FirecrackerVersion: sbx.Config.FirecrackerConfig.FirecrackerVersion,
	})
	err = lb.PauseAndUpload(
		ctx,
		userLogger,
		sbx,
		cmd.Hash,
		meta,
	)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("pause and upload: %w", err)
	}

	return meta, nil
}

// updateEnvdInSandbox updates the envd binary in the sandbox to the latest version.
func (lb *LayerExecutor) updateEnvdInSandbox(
	ctx context.Context,
	userLogger logger.Logger,
	sbx *sandbox.Sandbox,
) error {
	ctx, childSpan := tracer.Start(ctx, "update-envd")
	defer childSpan.End()

	envdVersion, err := envd.GetEnvdVersion(ctx, lb.BuilderConfig.HostEnvdPath)
	if err != nil {
		return fmt.Errorf("error getting envd version: %w", err)
	}
	userLogger.Debug(ctx, fmt.Sprintf("Updating envd to version v%s", envdVersion))

	// Step 1: Copy the updated envd binary from host to /tmp in sandbox
	tmpEnvdPath := "/tmp/envd_updated"
	err = sandboxtools.CopyFile(
		ctx,
		lb.proxy,
		sbx.Runtime.SandboxID,
		"root",
		lb.BuilderConfig.HostEnvdPath,
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
		lb.proxy,
		userLogger,
		zap.DebugLevel,
		"update-envd-replace",
		sbx.Runtime.SandboxID,
		replaceEnvdCmd,
		metadata.Context{User: "root"},
	)
	if err != nil {
		return fmt.Errorf("failed to replace envd binary: %w", err)
	}

	// Step 3: Restart the systemd envd service
	// Error is ignored because it's expected the envd connection will be lost
	_ = sandboxtools.RunCommand(
		ctx,
		lb.proxy,
		sbx.Runtime.SandboxID,
		"systemctl restart envd",
		metadata.Context{User: "root"},
	)

	// Remove the proxy client to prevent reuse of broken connection, because we restarted envd server inside of the sandbox.
	// This might not be necessary if we don't use keepalives for the proxy.
	err = lb.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
	if err != nil {
		// Errors here will be from forcefully closing the connections, so we can ignore them—they will at worst timeout on their own.
		lb.logger.Warn(ctx, "errors when manually closing connections to sandbox after restarting envd", zap.Error(err))
	}

	lb.logger.Debug(
		ctx,
		"removed proxy from pool after restarting envd",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithExecutionID(sbx.Runtime.ExecutionID),
	)

	// Step 4: Wait for envd to initialize
	err = sbx.WaitForEnvd(
		ctx,
		waitEnvdTimeout,
	)
	if err != nil {
		return fmt.Errorf("failed to wait for envd initialization after update: %w", err)
	}

	return nil
}

func (lb *LayerExecutor) PauseAndUpload(
	ctx context.Context,
	userLogger logger.Logger,
	sbx *sandbox.Sandbox,
	hash string,
	meta metadata.Template,
) (e error) {
	ctx, childSpan := tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	userLogger.Debug(ctx, fmt.Sprintf("Processing layer: %s", meta.Template.BuildID))

	// snapshot is automatically cleared by the templateCache eviction
	snapshot, err := sbx.Pause(
		ctx,
		meta,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	// Add snapshot to template cache so it can be used immediately
	err = lb.templateCache.AddSnapshot(
		context.WithoutCancel(ctx),
		meta.Template.BuildID,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.Metafile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)
	if err != nil {
		err = errors.Join(err, snapshot.Close(context.WithoutCancel(ctx)))

		return fmt.Errorf("error adding snapshot to template cache: %w", err)
	}

	// Upload snapshot async - it's already in the template cache for immediate use
	userLogger.Debug(ctx, fmt.Sprintf("Saving: %s", meta.Template.BuildID))

	lb.startUpload(ctx, snapshot, hash, meta.Template.BuildID, userLogger)

	return nil
}

// startUpload runs the two-phase upload in a goroutine, coordinating with other
// layers to cross-inject frame tables into headers.
func (lb *LayerExecutor) startUpload(
	ctx context.Context,
	snapshot *sandbox.Snapshot,
	hash string,
	buildID string,
	userLogger logger.Logger,
) {
	completeDataFileUpload, waitForAllDataFileUploads := lb.uploadTracker.StartDataFileUpload()
	completeUpload, waitForPreviousUploads := lb.uploadTracker.StartUpload()
	pending := lb.uploadTracker.Pending()

	lb.UploadErrGroup.Go(func() error {
		ctx := context.WithoutCancel(ctx)
		ctx, span := tracer.Start(ctx, "upload snapshot")
		defer span.End()
		defer completeUpload()

		// Phase 1: Upload data files
		result, err := snapshot.UploadDataFiles(ctx, lb.templateStorage, storage.TemplateFiles{BuildID: buildID})
		if err != nil {
			completeDataFileUpload()

			return fmt.Errorf("error uploading data files: %w", err)
		}

		// Add frame tables for other layers to reference
		if result.RootfsFrameTable != nil {
			pending.Add(buildID+"/rootfs.ext4", result.RootfsFrameTable)
		}
		if result.MemfileFrameTable != nil {
			pending.Add(buildID+"/memfile", result.MemfileFrameTable)
		}
		completeDataFileUpload()

		// Phase 2: Wait for all layers, then finalize headers
		if err := waitForAllDataFileUploads(ctx); err != nil {
			return fmt.Errorf("error waiting for data uploads: %w", err)
		}

		if err := result.TemplateBuild.FinalizeHeaders(ctx, pending); err != nil {
			return fmt.Errorf("error finalizing headers: %w", err)
		}

		// Wait for previous uploads before saving cache entry
		if err := waitForPreviousUploads(ctx); err != nil {
			return fmt.Errorf("error waiting for previous uploads: %w", err)
		}

		err = lb.index.SaveLayerMeta(ctx, hash, cache.LayerMetadata{
			Template: cache.Template{BuildID: buildID},
		})
		if err != nil {
			return fmt.Errorf("error saving layer meta: %w", err)
		}

		userLogger.Debug(ctx, fmt.Sprintf("Saved: %s", buildID))

		return nil
	})
}
