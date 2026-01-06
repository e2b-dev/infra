package prefetch

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var prefetchTimeout = 5 * time.Minute

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/prefetch")

// PrefetchBuilder implements the prefetch phase that runs after finalize.
// It resumes the template, waits for envd, and captures the dirty pages
// to store as prefetch mapping in the template metadata.
type PrefetchBuilder struct {
	buildcontext.BuildContext

	sandboxFactory  *sandbox.Factory
	templateStorage storage.StorageProvider
	templateCache   *sbxtemplate.Cache
	proxy           *proxy.SandboxProxy
	sandboxes       *sandbox.Map

	logger logger.Logger
}

func New(
	buildContext buildcontext.BuildContext,
	sandboxFactory *sandbox.Factory,
	templateStorage storage.StorageProvider,
	templateCache *sbxtemplate.Cache,
	proxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	logger logger.Logger,
) *PrefetchBuilder {
	return &PrefetchBuilder{
		BuildContext: buildContext,

		sandboxFactory:  sandboxFactory,
		templateStorage: templateStorage,
		templateCache:   templateCache,
		proxy:           proxy,
		sandboxes:       sandboxes,

		logger: logger,
	}
}

func (pb *PrefetchBuilder) Prefix() string {
	return "prefetch"
}

func (pb *PrefetchBuilder) String(context.Context) (string, error) {
	return "Collecting prefetch mapping", nil
}

func (pb *PrefetchBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhasePrefetch,
		StepType: "prefetch",
	}
}

func (pb *PrefetchBuilder) Hash(_ context.Context, sourceLayer phases.LayerResult) (string, error) {
	// The prefetch phase hash is based on the source layer hash
	// This ensures it runs after finalize but doesn't create a new cacheable layer
	return cache.HashKeys(sourceLayer.Hash, "prefetch"), nil
}

func (pb *PrefetchBuilder) Layer(
	_ context.Context,
	sourceLayer phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	// The prefetch phase doesn't create a new layer, it only updates metadata
	// Return the source layer with the new hash, marked as not cached
	return phases.LayerResult{
		Metadata: sourceLayer.Metadata,
		Cached:   false,
		Hash:     hash,
	}, nil
}

// Build runs the prefetch phase which:
// 1. Resumes the template from the finalize snapshot
// 2. Waits for envd to respond
// 3. Captures the dirty pages from uffd
// 4. Updates the metadata with the prefetch mapping
// 5. Uploads the updated metadata
func (pb *PrefetchBuilder) Build(
	ctx context.Context,
	userLogger logger.Logger,
	_ string,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build prefetch")
	defer span.End()

	userLogger.Info(ctx, "Collecting prefetch mapping from template resume")

	// Get the template from the finalize phase
	// isSnapshot=false, isBuilding=true since we're in a build phase
	localTemplate, err := pb.templateCache.GetTemplate(ctx, sourceLayer.Metadata.Template.BuildID, false, true)
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("failed to get template from cache: %w", err)
	}

	// Resume the sandbox from the finalize snapshot
	memoryPrefetchMapping, err := pb.collectMemoryPrefetchMapping(ctx, userLogger, localTemplate)
	if err != nil {
		// Log but don't fail the build - prefetch is an optimization
		pb.logger.Warn(ctx, "failed to collect prefetch mapping, continuing without prefetch",
			zap.Error(err),
		)

		return phases.LayerResult{
			Metadata: sourceLayer.Metadata,
			Cached:   false,
			Hash:     currentLayer.Hash,
		}, nil
	}

	// Update metadata with prefetch mapping
	updatedMetadata := sourceLayer.Metadata.WithPrefetch(&metadata.Prefetch{
		Memory: memoryPrefetchMapping,
	})

	// Upload the updated metadata
	err = updatedMetadata.Upload(ctx, pb.templateStorage)
	if err != nil {
		pb.logger.Warn(ctx, "failed to upload prefetch metadata, continuing without prefetch",
			zap.Error(err),
		)

		return phases.LayerResult{
			Metadata: sourceLayer.Metadata,
			Cached:   false,
			Hash:     currentLayer.Hash,
		}, nil
	}

	pageCount := uint(0)
	if memoryPrefetchMapping != nil {
		pages := memoryPrefetchMapping.Pages
		pageCount = pages.Count()
	}

	userLogger.Info(ctx, fmt.Sprintf("Collected prefetch mapping with %d pages", pageCount))

	return phases.LayerResult{
		Metadata: updatedMetadata,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (pb *PrefetchBuilder) collectMemoryPrefetchMapping(
	ctx context.Context,
	userLogger logger.Logger,
	localTemplate sbxtemplate.Template,
) (*metadata.PrefetchMapping, error) {
	ctx, span := tracer.Start(ctx, "collect-prefetch-mapping")
	defer span.End()

	// Configure sandbox for prefetch collection
	sbxConfig := sandbox.Config{
		Vcpu:      pb.Config.VCpuCount,
		RamMB:     pb.Config.MemoryMB,
		HugePages: pb.Config.HugePages,

		Envd: sandbox.EnvdMetadata{
			Version: pb.EnvdVersion,
		},

		FirecrackerConfig: fc.Config{
			KernelVersion:      pb.Config.KernelVersion,
			FirecrackerVersion: pb.Config.FirecrackerVersion,
		},
	}

	// Create sandbox creator for resuming
	sandboxCreator := layer.NewResumeSandbox(sbxConfig, pb.sandboxFactory, prefetchTimeout)

	// Create a minimal layer executor for sandbox creation
	layerExecutor := &layer.LayerExecutor{
		BuildContext: pb.BuildContext,
	}

	// Resume the sandbox
	sbx, err := sandboxCreator.Sandbox(ctx, layerExecutor, localTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to resume sandbox: %w", err)
	}
	defer sbx.Close(ctx)

	// Add to sandboxes map for proxy routing
	pb.sandboxes.Insert(sbx)
	defer func() {
		pb.sandboxes.Remove(sbx.Runtime.SandboxID)
		_ = pb.proxy.RemoveFromPool(sbx.Runtime.ExecutionID)
	}()

	// Wait for envd to be ready - this confirms the sandbox has fully started
	userLogger.Debug(ctx, "Waiting for envd to confirm sandbox start")
	err = sbx.WaitForEnvd(ctx, pb.sandboxFactory.GetEnvdInitRequestTimeout(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed waiting for envd: %w", err)
	}

	userLogger.Debug(ctx, "Envd responded, collecting dirty pages")

	// Get the dirty pages from the memory backend
	// These are the pages that were requested during the sandbox start
	diffMetadata, err := sbx.MemoryDiffMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff metadata: %w", err)
	}

	if diffMetadata.Dirty == nil || diffMetadata.Dirty.Count() == 0 {
		pb.logger.Debug(ctx, "no dirty pages found for prefetch mapping")

		return nil, nil
	}

	span.SetAttributes(
		attribute.Int64("dirty_pages", int64(diffMetadata.Dirty.Count())),
		attribute.Int64("block_size", diffMetadata.BlockSize),
	)

	// Create prefetch mapping from dirty pages
	return &metadata.PrefetchMapping{
		Pages:     diffMetadata.Dirty,
		BlockSize: diffMetadata.BlockSize,
	}, nil
}
