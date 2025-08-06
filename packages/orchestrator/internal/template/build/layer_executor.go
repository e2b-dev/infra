package build

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type LayerExecutor struct {
	BuildContext

	tracer trace.Tracer

	networkPool     *network.Pool
	devicePool      *nbd.DevicePool
	templateCache   *sbxtemplate.Cache
	proxy           *proxy.SandboxProxy
	sandboxes       *smap.Map[*sandbox.Sandbox]
	templateStorage storage.StorageProvider
	buildStorage    storage.StorageProvider
}

const layerTimeout = time.Hour

func NewLayerExecutor(
	buildContext BuildContext,
	tracer trace.Tracer,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	templateCache *sbxtemplate.Cache,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
) *LayerExecutor {
	return &LayerExecutor{
		BuildContext:    buildContext,

		tracer:          tracer,
		
		networkPool:     networkPool,
		devicePool:      devicePool,
		templateCache:   templateCache,
		proxy:           proxy,
		sandboxes:       sandboxes,
		templateStorage: templateStorage,
		buildStorage:    buildStorage,
	}
}

func ResumeSandbox(
	ctx context.Context,
	lb *LayerExecutor,
	template sbxtemplate.Template,
	sbxConfig sandbox.Config,
) (*sandbox.Sandbox, error) {
	sbx, err := sandbox.ResumeSandbox(
		ctx,
		lb.tracer,
		lb.networkPool,
		template,
		sbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		uuid.New().String(),
		time.Now(),
		time.Now().Add(layerTimeout),
		lb.devicePool,
		false,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("error resuming sandbox: %w", err)
	}
	return sbx, nil
}

func CreateSandboxFromTemplate(
	ctx context.Context,
	lb *LayerExecutor,
	template sbxtemplate.Template,
	sbxConfig sandbox.Config,
	exportTemplate storage.TemplateFiles,
) (*sandbox.Sandbox, error) {
	// Create new sandbox path
	var oldMemfile block.ReadonlyDevice
	oldMemfile, err := template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("error getting memfile from local template: %w", err)
	}

	// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
	// This is ok as the sandbox is started from the beginning.
	var memfile block.ReadonlyDevice
	memfile, err = block.NewEmpty(
		sbxConfig.RamMB<<constants.ToMBShift,
		oldMemfile.BlockSize(),
		uuid.MustParse(template.Files().BuildID),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating memfile: %w", err)
	}

	err = template.ReplaceMemfile(memfile)
	if err != nil {
		return nil, fmt.Errorf("error setting memfile for local template: %w", err)
	}

	// In case of a new sandbox, base template ID is now used as the potentially exported template base ID.
	sbxConfig.BaseTemplateID = exportTemplate.TemplateID
	sbx, err := sandbox.CreateSandbox(
		ctx,
		lb.tracer,
		lb.networkPool,
		lb.devicePool,
		sbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fc.FirecrackerVersions{
			KernelVersion:      exportTemplate.KernelVersion,
			FirecrackerVersion: exportTemplate.FirecrackerVersion,
		},
		template,
		layerTimeout,
		"",
		fc.ProcessOptions{
			InitScriptPath:      systemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating sandbox: %w", err)
	}

	err = sbx.WaitForEnvd(
		ctx,
		lb.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	return sbx, nil
}

// BuildLayer orchestrates the layer building process
func (lb *LayerExecutor) BuildLayer(
	ctx context.Context,
	hash string,
	sourceTemplate storage.TemplateFiles,
	exportTemplate storage.TemplateFiles,
	updateEnvd bool,
	getSandbox func(
		context context.Context,
		b *LayerExecutor,
		template sbxtemplate.Template,
		exportTemplate storage.TemplateFiles,
	) (*sandbox.Sandbox, error),
	fn func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error),
) (LayerMetadata, error) {
	ctx, childSpan := lb.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	localTemplate, err := lb.templateCache.GetTemplate(
		sourceTemplate.TemplateID,
		sourceTemplate.BuildID,
		sourceTemplate.KernelVersion,
		sourceTemplate.FirecrackerVersion,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to get template snapshot data: %w", err)
	}

	// Create or resume sandbox
	sbx, err := getSandbox(ctx, lb, localTemplate, exportTemplate)
	if err != nil {
		return LayerMetadata{}, err
	}
	defer sbx.Stop(ctx)

	// Add to proxy so we can call envd commands
	lb.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
	defer func() {
		lb.sandboxes.Remove(sbx.Runtime.SandboxID)
		lb.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
	}()

	// Update envd binary to the latest version
	if updateEnvd {
		err = lb.updateEnvdInSandbox(ctx, sbx)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("failed to update envd in sandbox: %w", err)
		}
	}

	// Execute the provided function
	meta, err := fn(ctx, sbx)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error running action in sandbox: %w", err)
	}

	// Prepare export metadata and upload
	exportMeta := LayerMetadata{
		Template: exportTemplate,
		CmdMeta:  meta,
	}
	err = lb.PauseAndUpload(
		ctx,
		sbx,
		hash,
		exportMeta,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error pausing and uploading template: %w", err)
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
	layerMeta LayerMetadata,
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

		err = saveLayerMeta(ctx, lb.buildStorage, lb.CacheScope, hash, layerMeta)
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		lb.UserLogger.Debug(fmt.Sprintf("Saved: %s/%s", cacheFiles.TemplateID, cacheFiles.BuildID))
		return nil
	})

	return nil
}
