package main

import (
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/ioc"
	"go.uber.org/fx"
)

const version = "0.1.0"

var commitSHA string

func main() {
	fx.New(
		fx.Provide(
			ioc.NewVersionInfo(version, commitSHA),
			ioc.NewConfig,
			ioc.NewState,
			ioc.NewTelemetry,
			ioc.NewGlobalLogger,
			ioc.NewFeatureFlagsClient,
			ioc.NewLimiter,
			ioc.NewPersistence,
			ioc.NewSandboxesMap,
			ioc.NewBlockMetrics,
			ioc.NewTemplateCache,
			ioc.NewSandboxEventBatcher,
			ioc.NewRedis,
			ioc.NewPubSub,
			ioc.NewSandboxEventsService,
			ioc.NewSandboxObserver,
			ioc.NewSandboxProxy,
			ioc.NewHyperloopServer,
			ioc.NewDevicePool,
			ioc.NewNetworkPool,
			ioc.NewSandboxFactory,
			ioc.NewOrchestratorService,
			ioc.NewServiceInfo,
			ioc.NewGRPCServer,
			ioc.NewInfoService,
			ioc.NewGRPCHealthServer,
			ioc.NewCMUXServer,
			ioc.NewHTTPServer,
			ioc.NewGRPCCMUXServer,
			ioc.NewTemplateManager,
		),
		fx.Invoke(
			ioc.NewSingleOrchestratorCheck,   // Lock file check for single orchestrator
			ioc.NewDrainingHandler,           // Graceful shutdown handler
			ioc.NewSandboxLoggerInternal,     // Initialize sandbox internal logger
			ioc.NewSandboxLoggerExternal,     // Initialize sandbox external logger
			func(ioc.HyperloopHTTPServer) {}, // Hyperloop HTTP server (independent)
			ioc.StartCMUXServer,              // Start CMUX (FX ensures this runs before HTTP/gRPC)
			func(ioc.HealthHTTPServer) {},    // Health HTTP server
			func(net.Listener) {},            // gRPC server
		),
	).Run()
}
