package ioc

import (
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"go.uber.org/fx"
)

func New(config cfg.Config, version, commitSHA string) *fx.App {
	return fx.New(
		fx.StartTimeout(15*time.Second),
		fx.StopTimeout(24*time.Hour),

		fx.Supply(config),

		NewClickhouseModule(config),

		NewGRPCModule(),
		NewHealthModule(),
		NewHyperloopModule(),
		NewObservabilityModule(),
		NewRedisModule(),
		NewSandboxesModule(),
		NewTemplateManagerModule(config),

		fx.Provide(
			NewVersionInfo(version, commitSHA),
			NewState,
			NewFeatureFlagsClient,
			NewLimiter,
			NewPersistence,
			NewBlockMetrics,
			NewTemplateCache,
			WithDeliveryTargets(NewSandboxEventsService),
			NewServiceInfo,
		),
		fx.Invoke(
			NewSingleOrchestratorCheck, // Lock file check for single orchestrator
			NewDrainingHandler,         // Graceful shutdown handler
			NewSandboxLoggerInternal,   // Initialize sandbox internal logger
			NewSandboxLoggerExternal,   // Initialize sandbox external logger
		),
	)
}
