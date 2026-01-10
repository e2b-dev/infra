package optimize

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
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

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/optimize")

// OptimizeBuilder resumes the template, waits for envd, and captures the dirty blocks
// to store as prefetch mapping in the template metadata.
type OptimizeBuilder struct {
	buildcontext.BuildContext

	layerExecutor   *layer.LayerExecutor
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
	layerExecutor *layer.LayerExecutor,
	sandboxes *sandbox.Map,
	logger logger.Logger,
) *OptimizeBuilder {
	return &OptimizeBuilder{
		BuildContext: buildContext,

		sandboxFactory:  sandboxFactory,
		templateStorage: templateStorage,
		templateCache:   templateCache,
		proxy:           proxy,
		layerExecutor:   layerExecutor,
		sandboxes:       sandboxes,

		logger: logger,
	}
}

func (pb *OptimizeBuilder) Prefix() string {
	return "optimize"
}

func (pb *OptimizeBuilder) String(context.Context) (string, error) {
	return "Optimizing template", nil
}

func (pb *OptimizeBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseOptimize,
		StepType: "optimize",
	}
}

func (pb *OptimizeBuilder) Hash(_ context.Context, sourceLayer phases.LayerResult) (string, error) {
	// The optimize phase hash is based on the source layer hash
	// This ensures it runs after finalize but doesn't create a new cacheable layer
	return cache.HashKeys(sourceLayer.Hash, "optimize"), nil
}

func (pb *OptimizeBuilder) Layer(
	_ context.Context,
	sourceLayer phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	// The optimize phase doesn't create a new layer, it only updates metadata
	// Return the source layer with the new hash, marked as not cached
	return phases.LayerResult{
		Metadata: sourceLayer.Metadata,
		Cached:   false,
		Hash:     hash,
	}, nil
}

// Build runs the optimize phase which:
// 1. Resumes the template from the finalize snapshot
// 2. Waits for envd to respond
// 3. Captures the dirty blocks from uffd
// 4. Updates the metadata with the prefetch mapping
// 5. Uploads the updated metadata
func (pb *OptimizeBuilder) Build(
	ctx context.Context,
	_ logger.Logger,
	_ string,
	sourceLayer phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build prefetch")
	defer span.End()

	pb.logger.Info(ctx, "Collecting prefetch mapping from template resume")

	// Get the template from the finalize phase
	// isSnapshot=false, isBuilding=true since we're in a build phase
	localTemplate, err := pb.templateCache.GetTemplate(ctx, sourceLayer.Metadata.Template.BuildID, false, true)
	if err != nil {
		return phases.LayerResult{}, fmt.Errorf("failed to get template from cache: %w", err)
	}

	// Resume the sandbox from the finalize snapshot
	memoryPrefetchMapping, err := pb.collectMemoryPrefetchMapping(ctx, localTemplate)
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

	blockCount := 0
	if memoryPrefetchMapping != nil {
		blockCount = memoryPrefetchMapping.Count()
	}

	pb.logger.Info(ctx, fmt.Sprintf("Collected prefetch mapping with %d memory blocks", blockCount))

	return phases.LayerResult{
		Metadata: updatedMetadata,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (pb *OptimizeBuilder) collectMemoryPrefetchMapping(
	ctx context.Context,
	localTemplate sbxtemplate.Template,
) (*metadata.MemoryPrefetchMapping, error) {
	ctx, span := tracer.Start(ctx, "collect prefetch-mapping")
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

	// Resume the sandbox
	sbx, err := sandboxCreator.Sandbox(ctx, pb.layerExecutor, localTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to resume sandbox: %w", err)
	}
	defer sbx.Close(ctx)

	// Get the prefetch data from the memory backend
	prefetchData, err := sbx.MemoryPrefetchData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get prefetch data: %w", err)
	}

	if len(prefetchData.BlockEntries) == 0 {
		pb.logger.Debug(ctx, "no blocks found for prefetch mapping")

		return nil, nil
	}

	span.SetAttributes(
		attribute.Int64("prefetch_blocks", int64(len(prefetchData.BlockEntries))),
		attribute.Int64("block_size", prefetchData.BlockSize),
	)

	// Collect entries and sort by Order to get ordered indices
	entries := make([]block.BlockEntry, 0, len(prefetchData.BlockEntries))
	for _, entry := range prefetchData.BlockEntries {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b block.BlockEntry) int {
		if a.Order < b.Order {
			return -1
		}
		if a.Order > b.Order {
			return 1
		}

		return 0
	})

	// Build ordered indices and metadata
	orderedIndices := make([]uint64, len(entries))
	blockMetadata := make(map[uint64]metadata.BlockMetadata, len(entries))
	for i, entry := range entries {
		orderedIndices[i] = entry.Index
		blockMetadata[entry.Index] = metadata.BlockMetadata{
			Order:      float64(entry.Order),
			AccessType: entry.AccessType,
		}
	}

	// Create prefetch mapping with ordered indices and metadata
	return &metadata.MemoryPrefetchMapping{
		Indices:   orderedIndices,
		Metadata:  blockMetadata,
		BlockSize: prefetchData.BlockSize,
	}, nil
}
