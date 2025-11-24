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

func build(config cfg.Config, vInfo VersionInfo) []fx.Option {
	return []fx.Option{
		fx.StartTimeout(15 * time.Second),
		fx.StopTimeout(24 * time.Hour),

		fx.Supply(vInfo),
		fx.Supply(config),

		newCMUXModule(),
		newClickhouseModule(config),
		newGRPCModule(),
		newHealthModule(),
		newHyperloopModule(),
		newNetworkModule(),
		newObservabilityModule(),
		newRedisModule(config),
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
	}
}

func Validate(config cfg.Config, version, commitSHA string) error {
	vInfo := VersionInfo{Version: version, Commit: commitSHA}

	options := build(config, vInfo)

	return fx.ValidateApp(options...)
}

func New(config cfg.Config, version, commitSHA string, opts ...fx.Option) *fx.App {
	vInfo := VersionInfo{Version: version, Commit: commitSHA}

	options := build(config, vInfo)

	options = append(options, opts...)

	return fx.New(options...)
}
