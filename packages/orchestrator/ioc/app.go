package ioc

import (
	"time"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

type VersionInfo struct {
	Version string
	Commit  string
}

func New(config cfg.Config, version, commitSHA string) *fx.App {
	vInfo := VersionInfo{Version: version, Commit: commitSHA}

	return fx.New(
		fx.StartTimeout(15*time.Second),
		fx.StopTimeout(24*time.Hour),

		fx.Supply(vInfo),
		fx.Supply(config),

		newCMUXModule(),
		newClickhouseModule(config),
		newGRPCModule(),
		newHealthModule(),
		newHyperloopModule(),
		newObservabilityModule(),
		newRedisModule(),
		newSandboxesModule(),
		newTemplateManagerModule(config),

		newDebugGraphModule(),

		fx.Provide(
			newState,
			newFeatureFlagsClient,
			newLimiter,
			newPersistence,
			newBlockMetrics,
			newTemplateCache,
			withDeliveryTargets(newSandboxEventsService),
			newServiceInfo,
		),
		fx.Invoke(
			newSandboxLoggerInternal, // Initialize sandbox internal logger
			newSandboxLoggerExternal, // Initialize sandbox external logger
		),
	)
}
