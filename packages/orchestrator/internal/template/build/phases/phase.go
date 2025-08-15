package phases

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

type PhaseMeta struct {
	Phase    metrics.Phase
	StepType string
}

type BuilderPhase interface {
	Prefix() string
	String(ctx context.Context) (string, error)
	Metadata() PhaseMeta

	Hash(sourceLayer LayerResult) (string, error)
	Layer(ctx context.Context, sourceLayer LayerResult, hash string) (LayerResult, error)
	Build(ctx context.Context, sourceLayer LayerResult, currentLayer LayerResult) (LayerResult, error)
}

type LayerResult struct {
	Metadata cache.LayerMetadata
	Cached   bool
	Hash     string

	StartMetadata *metadata.StartMetadata
}

func layerInfo(
	cached bool,
	prefix string,
	text string,
	hash string,
) string {
	cachedPrefix := ""
	if cached {
		cachedPrefix = "CACHED "
	}
	return fmt.Sprintf("%s[%s] %s [%s]", cachedPrefix, prefix, text, hash)
}

func Run(
	ctx context.Context,
	bc buildcontext.BuildContext,
	metrics *metrics.BuildMetrics,
	builders []BuilderPhase,
) (LayerResult, error) {
	sourceLayer := LayerResult{}

	for _, builder := range builders {
		meta := builder.Metadata()

		phaseStartTime := time.Now()
		hash, err := builder.Hash(sourceLayer)
		if err != nil {
			return LayerResult{}, fmt.Errorf("hash get failed for %s: %w", meta.Phase, err)
		}

		currentLayer, err := builder.Layer(ctx, sourceLayer, hash)
		if err != nil {
			return LayerResult{}, fmt.Errorf("metadata get failed for %s: %w", meta.Phase, err)
		}
		metrics.RecordCacheResult(ctx, meta.Phase, meta.StepType, currentLayer.Cached)

		prefix := builder.Prefix()
		source, err := builder.String(ctx)
		if err != nil {
			return LayerResult{}, fmt.Errorf("string get failed for %s: %w", meta.Phase, err)
		}
		bc.UserLogger.Info(layerInfo(currentLayer.Cached, prefix, source, currentLayer.Hash))

		if currentLayer.Cached {
			phaseDuration := time.Since(phaseStartTime)
			metrics.RecordPhaseDuration(ctx, phaseDuration, meta.Phase, meta.StepType, true)

			sourceLayer = currentLayer
			continue
		}

		res, err := builder.Build(ctx, sourceLayer, currentLayer)
		// Record phase duration
		phaseDuration := time.Since(phaseStartTime)
		metrics.RecordPhaseDuration(ctx, phaseDuration, meta.Phase, meta.StepType, false)

		if err != nil {
			return LayerResult{}, fmt.Errorf("error building phase %s: %w", meta.Phase, err)
		}

		sourceLayer = res
	}

	return sourceLayer, nil
}
