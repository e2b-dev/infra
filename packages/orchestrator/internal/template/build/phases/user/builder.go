package user

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/steps"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type UserBuilder struct {
	*steps.StepBuilder

	user string
}

func New(
	buildContext buildcontext.BuildContext,
	sandboxFactory *sandbox.Factory,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	commandExecutor *commands.CommandExecutor,
	index cache.Index,
	metrics *metrics.BuildMetrics,
	user string,
) *UserBuilder {
	return &UserBuilder{
		StepBuilder: steps.New(
			buildContext,
			sandboxFactory,
			logger,
			proxy,
			layerExecutor,
			commandExecutor,
			index,
			metrics,
			&template_manager.TemplateStep{
				Type: "USER",
				Args: []string{user},
			},
			// This step number shouldn't be used, but in case it does, defining as 1
			1,
		),
		user: user,
	}
}

func (ub *UserBuilder) Prefix() string {
	return "base"
}

func (ub *UserBuilder) String(_ context.Context) (string, error) {
	return fmt.Sprintf("DEFAULT USER %s", ub.user), nil
}

func (ub *UserBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseBase,
		StepType: "base",
	}
}
