package phases

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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
	Metadata metadata.Template
	Cached   bool
	Hash     string
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
			return LayerResult{}, fmt.Errorf("getting hash: %w", err)
		}

		currentLayer, err := builder.Layer(ctx, sourceLayer, hash)
		if err != nil {
			return LayerResult{}, fmt.Errorf("getting layer: %w", err)
		}
		metrics.RecordCacheResult(ctx, meta.Phase, meta.StepType, currentLayer.Cached)

		prefix := builder.Prefix()
		source, err := builder.String(ctx)
		if err != nil {
			return LayerResult{}, fmt.Errorf("getting source: %w", err)
		}
		bc.UserLogger.Info(layerInfo(currentLayer.Cached, prefix, source, currentLayer.Hash))

		if currentLayer.Cached {
			phaseDuration := time.Since(phaseStartTime)
			metrics.RecordPhaseDuration(ctx, phaseDuration, meta.Phase, meta.StepType, true)

			sourceLayer = currentLayer
			continue
		}

		err = validateLayer(currentLayer)
		if err != nil {
			return LayerResult{}, fmt.Errorf("validating layer: %w", err)
		}

		res, err := builder.Build(ctx, sourceLayer, currentLayer)
		// Record phase duration
		phaseDuration := time.Since(phaseStartTime)
		metrics.RecordPhaseDuration(ctx, phaseDuration, meta.Phase, meta.StepType, false)

		if err != nil {
			return LayerResult{}, err
		}

		sourceLayer = res
	}

	return sourceLayer, nil
}

func validateLayer(
	layer LayerResult,
) error {
	if layer.Hash == "" {
		return fmt.Errorf("layer hash is empty")
	}

	return validateMetadata(layer.Metadata)
}

func validateMetadata(
	meta metadata.Template,
) error {
	err := validateTemplate(meta.Template)
	if err != nil {
		return err
	}

	return validateContext(meta.Context)
}

func validateTemplate(
	files storage.TemplateFiles,
) error {
	if files.BuildID == "" {
		return fmt.Errorf("template build ID is empty")
	}
	if files.KernelVersion == "" {
		return fmt.Errorf("template kernel version is empty")
	}
	if files.FirecrackerVersion == "" {
		return fmt.Errorf("template firecracker version is empty")
	}

	return nil
}

func validateContext(
	context metadata.Context,
) error {
	if context.User == "" {
		return fmt.Errorf("context user is empty")
	}
	if context.WorkDir != nil && *context.WorkDir == "" {
		return fmt.Errorf("context working dir is empty")
	}

	return nil
}
