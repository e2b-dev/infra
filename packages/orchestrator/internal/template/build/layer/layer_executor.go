package layer

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type LayerExecutor struct {
	buildcontext.BuildContext

	tracer trace.Tracer

	networkPool     *network.Pool
	devicePool      *nbd.DevicePool
	templateCache   *sbxtemplate.Cache
	proxy           *proxy.SandboxProxy
	sandboxes       *smap.Map[*sandbox.Sandbox]
	templateStorage storage.StorageProvider
	buildStorage    storage.StorageProvider
	index           cache.Index
}

func NewLayerExecutor(
	buildContext buildcontext.BuildContext,
	tracer trace.Tracer,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	templateCache *sbxtemplate.Cache,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	index cache.Index,
) *LayerExecutor {
	return &LayerExecutor{
		BuildContext: buildContext,

		tracer: tracer,

		networkPool:     networkPool,
		devicePool:      devicePool,
		templateCache:   templateCache,
		proxy:           proxy,
		sandboxes:       sandboxes,
		templateStorage: templateStorage,
		buildStorage:    buildStorage,
		index:           index,
	}
}

// BuildLayer orchestrates the layer building process
func (lb *LayerExecutor) BuildLayer(
	ctx context.Context,
	cmd LayerBuildCommand,
) (cache.LayerMetadata, error) {
	ctx, childSpan := lb.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	localTemplate, err := lb.templateCache.GetTemplate(
		cmd.SourceLayer.Template.TemplateID,
		cmd.SourceLayer.Template.BuildID,
		cmd.SourceLayer.Template.KernelVersion,
		cmd.SourceLayer.Template.FirecrackerVersion,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("get template snapshot: %w", err)
	}

	// Create or resume sandbox
	sbx, err := cmd.SandboxCreator.Sandbox(ctx, lb, localTemplate)
	if err != nil {
		return cache.LayerMetadata{}, err
	}
	defer sbx.Stop(ctx)

	// Add to proxy so we can call envd commands
	lb.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
	defer func() {
		lb.sandboxes.Remove(sbx.Runtime.SandboxID)
		lb.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
	}()

	// Update envd binary to the latest version
	if cmd.UpdateEnvd {
		err = lb.updateEnvdInSandbox(ctx, sbx)
		if err != nil {
			return cache.LayerMetadata{}, fmt.Errorf("update envd: %w", err)
		}
	}

	// Execute the action using the executor
	meta, err := cmd.ActionExecutor.Execute(ctx, sbx, cmd.SourceLayer.CmdMeta)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("execute action: %w", err)
	}

	// Prepare export metadata and upload
	fcVersions := sbx.FirecrackerVersions()
	exportMeta := cache.LayerMetadata{
		Template: storage.TemplateFiles{
			TemplateID:         cmd.ExportTemplate.TemplateID,
			BuildID:            cmd.ExportTemplate.BuildID,
			KernelVersion:      fcVersions.KernelVersion,
			FirecrackerVersion: fcVersions.FirecrackerVersion,
		},
		CmdMeta: meta,
	}
	err = lb.PauseAndUpload(
		ctx,
		sbx,
		cmd.Hash,
		exportMeta,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("pause and upload: %w", err)
	}

	return exportMeta, nil
}

// updateEnvdInSandbox updates the envd binary in the sandbox to the latest version.
func (lb *LayerExecutor) updateEnvdInSandbox(
	ctx context.Context,
	sbx *sandbox.Sandbox,
) error {
	ctx, childSpan := lb.tracer.Start(ctx, "update-envd")
	defer childSpan.End()

	envdVersion, err := envd.GetEnvdVersion(ctx)
	if err != nil {
		return fmt.Errorf("error getting envd version: %w", err)
	}
	lb.UserLogger.Debug(fmt.Sprintf("Updating envd to version v%s", envdVersion))

	// Step 1: Copy the updated envd binary from host to /tmp in sandbox
	tmpEnvdPath := "/tmp/envd_updated"
	err = sandboxtools.CopyFile(
		ctx,
		lb.tracer,
		lb.proxy,
		sbx.Runtime.SandboxID,
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
		lb.tracer,
		lb.proxy,
		lb.UserLogger,
		zap.DebugLevel,
		"update-envd-replace",
		sbx.Runtime.SandboxID,
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
		lb.tracer,
		lb.proxy,
		sbx.Runtime.SandboxID,
		"systemctl restart envd",
		sandboxtools.CommandMetadata{User: "root"},
	)

	// Step 4: Wait for envd to initialize
	err = sbx.WaitForEnvd(
		ctx,
		lb.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return fmt.Errorf("failed to wait for envd initialization after update: %w", err)
	}

	return nil
}

func (lb *LayerExecutor) PauseAndUpload(
	ctx context.Context,
	sbx *sandbox.Sandbox,
	hash string,
	layerMeta cache.LayerMetadata,
) error {
	ctx, childSpan := lb.tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	lb.UserLogger.Debug(fmt.Sprintf("Saving layer: %s/%s", layerMeta.Template.TemplateID, layerMeta.Template.BuildID))

	cacheFiles, err := layerMeta.Template.CacheFiles()
	if err != nil {
		return fmt.Errorf("error creating template files: %w", err)
	}
	// snapshot is automatically cleared by the templateCache eviction
	snapshot, err := sbx.Pause(
		ctx,
		lb.tracer,
		cacheFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	// Add snapshot to template cache so it can be used immediately
	err = lb.templateCache.AddSnapshot(
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
	lb.UploadErrGroup.Go(func() error {
		err := snapshot.Upload(
			ctx,
			lb.templateStorage,
			cacheFiles.TemplateFiles,
		)
		if err != nil {
			return fmt.Errorf("error uploading snapshot: %w", err)
		}

		err = lb.index.SaveLayerMeta(ctx, hash, layerMeta)
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		lb.UserLogger.Debug(fmt.Sprintf("Saved: %s/%s", cacheFiles.TemplateID, cacheFiles.BuildID))
		return nil
	})

	return nil
}
