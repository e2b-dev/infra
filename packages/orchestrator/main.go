package main

import (
	"log"
	"time"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/ioc"
)

const version = "0.1.0"

var commitSHA string

func main() {
	config, err := ioc.NewConfig()
	if err != nil {
		log.Fatal(err)
	}

	fx.New(
		fx.StartTimeout(15*time.Second),
		fx.StopTimeout(24*time.Hour),

		fx.Supply(config),

		ioc.NewClickhouseModule(config),

		ioc.NewGRPCModule(),
		ioc.NewHealthModule(),
		ioc.NewHyperloopModule(),
		ioc.NewObservabilityModule(),
		ioc.NewRedisModule(),
		ioc.NewSandboxesModule(),
		ioc.NewTemplateManagerModule(config),

		fx.Provide(
			ioc.NewVersionInfo(version, commitSHA),
			ioc.NewState,
			ioc.NewFeatureFlagsClient,
			ioc.NewLimiter,
			ioc.NewPersistence,
			ioc.NewBlockMetrics,
			ioc.NewTemplateCache,
			ioc.WithDeliveryTargets(ioc.NewSandboxEventsService),
			ioc.NewServiceInfo,
		),
		fx.Invoke(
			ioc.NewSingleOrchestratorCheck, // Lock file check for single orchestrator
			ioc.NewDrainingHandler,         // Graceful shutdown handler
			ioc.NewSandboxLoggerInternal,   // Initialize sandbox internal logger
			ioc.NewSandboxLoggerExternal,   // Initialize sandbox external logger
		),
	).Run()
}
