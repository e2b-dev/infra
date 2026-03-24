package user

import (
	"context"
	"fmt"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases/steps"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type UserBuilder struct {
	*steps.StepBuilder

	user string
}

func New(
	buildContext buildcontext.BuildContext,
	sandboxFactory *sandbox.Factory,
	logger logger.Logger,
	proxy *proxy.SandboxProxy,
	layerExecutor *layer.LayerExecutor,
	commandExecutor *commands.CommandExecutor,
	index cache.Index,
	metrics *metrics.BuildMetrics,
	featureFlags *featureflags.Client,
	user string,
	force *bool,
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
			featureFlags,
			&template_manager.TemplateStep{
				Type:  "USER",
				Args:  []string{user, "true"},
				Force: force,
			},
			// This step number shouldn't be used, but in case it does, defining as 1
			1,
			zapcore.DebugLevel,
		),
		user: user,
	}
}

func (ub *UserBuilder) Prefix() string {
	return "base"
}

func (ub *UserBuilder) String(_ context.Context) (string, error) {
	return fmt.Sprintf("%s %s", prefix, ub.user), nil
}

func (ub *UserBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseBase,
		StepType: "base",
	}
}
