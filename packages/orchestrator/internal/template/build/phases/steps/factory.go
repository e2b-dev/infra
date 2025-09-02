package steps

import (
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
)

func CreateStepPhases(
	bc buildcontext.BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	commandExecutor *commands.CommandExecutor,
	index cache.Index,
	metrics *metrics.BuildMetrics,
) []phases.BuilderPhase {
	steps := make([]phases.BuilderPhase, 0, len(bc.Config.Steps))

	for i, step := range bc.Config.Steps {
		steps = append(steps,
			New(
				bc,
				logger,
				tracer,
				proxy,
				layerExecutor,
				commandExecutor,
				index,
				metrics,
				step,
				i+1, // stepNumber starts from 1
			),
		)
	}

	return steps
}
