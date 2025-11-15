package main

import (
	"log"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/ioc"
	"go.uber.org/fx"
)

const version = "0.1.0"

var commitSHA string

func main() {
	config, err := ioc.NewConfig()
	if err != nil {
		log.Fatal(err)
	}

	fx.New(
		fx.Supply(config),

		ioc.NewClickhouseModule(config),
		ioc.NewHyperloopModule(),
		ioc.NewRedisModule(),

		ioc.NewHealthModule(),

		ioc.NewTemplateManagerModule(config),

		fx.Provide(
			ioc.NewVersionInfo(version, commitSHA),
			ioc.NewState,
			ioc.NewTelemetry,
			ioc.NewGlobalLogger,
			ioc.NewFeatureFlagsClient,
			ioc.NewLimiter,
			ioc.NewPersistence,
			ioc.NewSandboxesMap,
			ioc.NewBlockMetrics,
			ioc.NewTemplateCache,
			ioc.WithDeliveryTargets(ioc.NewSandboxEventsService),
			ioc.NewSandboxObserver,
			ioc.NewSandboxProxy,
			ioc.NewDevicePool,
			ioc.NewNetworkStorage,
			ioc.NewNetworkPool,
			ioc.NewSandboxFactory,
			ioc.NewOrchestratorService,
			ioc.NewServiceInfo,
			ioc.NewGRPCServer,
			ioc.NewInfoService,
			ioc.NewCMUXServer,
			ioc.NewGRPCCMUXServer,
		),
		fx.Invoke(
			ioc.NewSingleOrchestratorCheck, // Lock file check for single orchestrator
			ioc.NewDrainingHandler,         // Graceful shutdown handler
			ioc.NewSandboxLoggerInternal,   // Initialize sandbox internal logger
			ioc.NewSandboxLoggerExternal,   // Initialize sandbox external logger
			ioc.StartCMUXServer,            // Start CMUX (FX ensures this runs before HTTP/gRPC)
			func(net.Listener) {},          // gRPC server
		),
	).Run()
}
