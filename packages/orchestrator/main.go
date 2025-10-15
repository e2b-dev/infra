package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	e2bhealthcheck "github.com/e2b-dev/infra/packages/orchestrator/internal/healthcheck"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/events/event"
	"github.com/e2b-dev/infra/packages/shared/pkg/events/webhooks"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/pubsub"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/supervisor"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const version = "0.1.0"

var commitSHA string

func main() {
	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	success := run(config)

	log.Println("Stopping orchestrator, success:", success)

	if success == false {
		os.Exit(1)
	}
}

func run(config cfg.Config) (success bool) {
	success = true

	services := cfg.GetServices(config)

	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	// We don't want to lock if the service is running with force stop; the subsequent start would fail.
	if !env.IsDevelopment() && !config.ForceStop && slices.Contains(services, cfg.Orchestrator) {
		fileLockName := config.OrchestratorLockPath
		info, err := os.Stat(fileLockName)
		if err == nil {
			log.Fatalf("Orchestrator was already started at %s, exiting", info.ModTime())
		}

		f, err := os.Create(fileLockName)
		if err != nil {
			log.Fatalf("Failed to create lock file %s: %v", fileLockName, err)
		}
		defer func() {
			fileErr := f.Close()
			if fileErr != nil {
				log.Printf("Failed to close lock file %s: %v", fileLockName, fileErr)
			}

			// Remove the lock file on graceful shutdown
			if success == true {
				if fileErr = os.Remove(fileLockName); fileErr != nil {
					log.Printf("Failed to remove lock file %s: %v", fileLockName, fileErr)
				}
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeID := env.GetNodeID()
	serviceName := cfg.GetServiceName(services)
	serviceInstanceID := uuid.NewString()
	serviceInfo := service.NewInfoContainer(nodeID, version, commitSHA, serviceInstanceID, config)

	serviceError := make(chan error)
	defer close(serviceError)

	var g errgroup.Group
	// defer waiting on the group so that this runs even when
	// there's a panic.
	defer func(g *errgroup.Group) {
		err := g.Wait()
		if err != nil {
			log.Printf("error while shutting down: %v", err)
			success = false
		}
	}(&g)

	// Setup telemetry
	var tel *telemetry.Client
	if telemetry.OtelCollectorGRPCEndpoint == "" {
		tel = telemetry.NewNoopClient()
	} else {
		var err error
		tel, err = telemetry.New(ctx, nodeID, serviceName, commitSHA, version, serviceInstanceID)
		if err != nil {
			zap.L().Fatal("failed to init telemetry", zap.Error(err))
		}
	}
	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("error while shutting down telemetry: %v", err)
			success = false
		}
	}()

	globalLogger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
	}))
	zap.ReplaceGlobals(globalLogger)
	runner := supervisor.New(globalLogger)
	runner.AddTask(
		"global logger",
		supervisor.WithCleanup(cleanupLogger(globalLogger)),
	)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)
	runner.AddTask(
		"sandbox logger (external)",
		supervisor.WithCleanup(cleanupLogger(sbxLoggerExternal)),
	)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       true,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)
	runner.AddTask(
		"sandbox logger (internal)",
		supervisor.WithCleanup(cleanupLogger(sbxLoggerInternal)),
	)

	globalLogger.Info("Starting orchestrator", zap.String("version", version), zap.String("commit", commitSHA), logger.WithServiceInstanceID(serviceInstanceID))

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := sandbox.NewSandboxesMap()

	// feature flags
	featureFlags, err := featureflags.NewClient()
	if err != nil {
		zap.L().Fatal("failed to create feature flags client", zap.Error(err))
	}
	runner.AddTask(
		"feature flags",
		supervisor.WithCleanup(func(ctx context.Context) error {
			return featureFlags.Close(ctx)
		}),
	)

	// gcp concurrent upload limiter
	limiter, err := limit.New(ctx, featureFlags)
	if err != nil {
		zap.L().Fatal("failed to create limiter", zap.Error(err))
	}
	runner.AddTask("gcp concurrent upload limiter", supervisor.WithCleanup(limiter.Close))

	persistence, err := storage.GetTemplateStorageProvider(ctx, limiter)
	if err != nil {
		zap.L().Fatal("failed to create template storage provider", zap.Error(err))
	}

	blockMetrics, err := blockmetrics.NewMetrics(tel.MeterProvider)
	if err != nil {
		zap.L().Fatal("failed to create metrics provider", zap.Error(err))
	}

	templateCache, err := template.NewCache(ctx, config, featureFlags, persistence, blockMetrics)
	if err != nil {
		zap.L().Fatal("failed to create template cache", zap.Error(err))
	}

	var sandboxEventBatcher batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent]

	clickhouseConnectionString := config.ClickhouseConnectionString
	if clickhouseConnectionString == "" {
		sandboxEventBatcher = batcher.NewNoopBatcher[clickhouse.SandboxEvent]()
	} else {
		clickhouseConn, err := clickhouse.NewDriver(clickhouseConnectionString)
		if err != nil {
			zap.L().Fatal("failed to create clickhouse driver", zap.Error(err))
		}

		sandboxEventBatcher, err = factories.NewSandboxInsertsEventBatcher(ctx, clickhouseConn, featureFlags)
		if err != nil {
			zap.L().Fatal("failed to create clickhouse batcher", zap.Error(err))
		}
	}
	runner.AddTask("sandbox event batcher", supervisor.WithCleanup(sandboxEventBatcher.Close))

	// redis
	redisClient, err := factories.NewRedisClient(ctx, config)
	if err != nil && !errors.Is(err, factories.ErrRedisDisabled) {
		zap.L().Fatal("Could not connect to Redis", zap.Error(err))
	} else if err == nil {
		runner.AddTask("redis client", supervisor.WithCleanup(func(context.Context) error {
			return factories.CloseCleanly(redisClient)
		}))
	}

	// pubsub
	var redisPubSub pubsub.PubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]
	if redisClient != nil {
		redisPubSub = pubsub.NewRedisPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData](redisClient, "sandbox-webhooks")
	} else {
		redisPubSub = pubsub.NewMockPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]()
	}
	runner.AddTask("redis pubsub", supervisor.WithCleanup(redisPubSub.Close))

	sbxEventsService := events.NewSandboxEventsService(featureFlags, redisPubSub, sandboxEventBatcher, globalLogger)

	// sandbox observer
	sandboxObserver, err := metrics.NewSandboxObserver(ctx, nodeID, serviceName, commitSHA, version, serviceInstanceID, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox observer", zap.Error(err))
	}
	runner.AddTask("sandbox observer", supervisor.WithCleanup(sandboxObserver.Close))

	// sandbox proxy
	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, config.ProxyPort, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox proxy", zap.Error(err))
	}
	runner.AddTask("sandbox proxy",
		supervisor.WithBackgroundJob(func(ctx context.Context) error {
			err := sandboxProxy.Start(ctx)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}),
		supervisor.WithCleanup(sandboxProxy.Close),
	)

	// device pool
	devicePool, err := nbd.NewDevicePool(tel.MeterProvider)
	if err != nil {
		zap.L().Fatal("failed to create device pool", zap.Error(err))
	}
	runner.AddTask("device pool",
		supervisor.WithBackgroundJob(func(ctx context.Context) error {
			devicePool.Populate(ctx)
			return nil
		}),
		supervisor.WithCleanup(devicePool.Close),
	)

	// network pool
	networkPool, err := network.NewPool(tel.MeterProvider, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, nodeID, config.NetworkConfig)
	if err != nil {
		zap.L().Fatal("failed to create network pool", zap.Error(err))
	}
	runner.AddTask("network pool", supervisor.WithBackgroundJob(func(ctx context.Context) error {
		networkPool.Populate(ctx)
		return nil
	}), supervisor.WithCleanup(networkPool.Close))

	// sandbox factory
	defaultAllowSandboxInternet := config.AllowSandboxInternet
	sandboxFactory := sandbox.NewFactory(networkPool, devicePool, featureFlags, defaultAllowSandboxInternet)

	orchestratorService := server.New(server.ServiceConfig{
		SandboxFactory:   sandboxFactory,
		Tel:              tel,
		NetworkPool:      networkPool,
		DevicePool:       devicePool,
		TemplateCache:    templateCache,
		Info:             serviceInfo,
		Proxy:            sandboxProxy,
		Sandboxes:        sandboxes,
		Persistence:      persistence,
		FeatureFlags:     featureFlags,
		SbxEventsService: sbxEventsService,
	})

	// template manager sandbox logger
	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceNameTemplate,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	runner.AddTask("template manager sandbox logger (external)",
		supervisor.WithCleanup(cleanupLogger(tmplSbxLoggerExternal)),
	)

	// hyperloop server
	hyperloopSrv, err := hyperloopserver.NewHyperloopServer(ctx, config.NetworkConfig.HyperloopProxyPort, globalLogger, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create hyperloop server", zap.Error(err))
	}
	runner.AddTask("hyperloop server",
		supervisor.WithBackgroundJob(func(context.Context) error {
			err := hyperloopSrv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}),
		supervisor.WithCleanup(hyperloopSrv.Shutdown),
	)

	grpcServer := factories.NewGRPCServer(tel)
	orchestrator.RegisterSandboxServiceServer(grpcServer, orchestratorService)

	// template manager
	if slices.Contains(services, cfg.TemplateManager) {
		tmpl, err := tmplserver.New(
			ctx,
			tel.MeterProvider,
			globalLogger,
			tmplSbxLoggerExternal,
			sandboxFactory,
			sandboxProxy,
			sandboxes,
			templateCache,
			persistence,
			limiter,
			serviceInfo,
		)
		if err != nil {
			zap.L().Fatal("failed to create template manager", zap.Error(err))
		}

		templatemanager.RegisterTemplateServiceServer(grpcServer, tmpl)

		runner.AddTask("template manager", supervisor.WithCleanup(tmpl.Close))
	}

	infoService := service.NewInfoService(serviceInfo, sandboxes)
	orchestratorinfo.RegisterInfoServiceServer(grpcServer, infoService)

	grpcHealth := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealth)

	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(ctx, config.GRPCPort)
	if err != nil {
		zap.L().Fatal("failed to create cmux server", zap.Error(err))
	}
	runner.AddTask("cmux server",
		supervisor.WithBackgroundJob(func(context.Context) error {
			zap.L().Info("Starting network server", zap.Uint16("port", config.GRPCPort))
			err := cmuxServer.Serve()
			if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			return err
		}),
		supervisor.WithCleanup(func(context.Context) error {
			zap.L().Info("Shutting down cmux server")
			cmuxServer.Close()
			return nil
		}),
	)

	// http server
	httpListener := cmuxServer.Match(cmux.HTTP1Fast())

	healthcheck, err := e2bhealthcheck.NewHealthcheck(serviceInfo)
	if err != nil {
		zap.L().Fatal("failed to create healthcheck", zap.Error(err))
	}

	httpServer := factories.NewHTTPServer()
	httpServer.Handler = healthcheck.CreateHandler()

	runner.AddTask("http server",
		supervisor.WithBackgroundJob(func(context.Context) error {
			err := httpServer.Serve(httpListener)
			switch {
			case errors.Is(err, cmux.ErrServerClosed):
				return nil
			case errors.Is(err, http.ErrServerClosed):
				return nil
			default:
				return err
			}
		}),
		supervisor.WithCleanup(func(ctx context.Context) error {
			zap.L().Info("Shutting down http server")
			return httpServer.Shutdown(ctx)
		}),
	)

	// grpc server
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests
	runner.AddTask("grpc server",
		supervisor.WithBackgroundJob(func(context.Context) error {
			return grpcServer.Serve(grpcListener)
		}),
		supervisor.WithCleanup(func(context.Context) error {
			zap.L().Info("Shutting down grpc server")
			grpcServer.GracefulStop()
			return nil
		}),
	)

	// Wait for the shutdown signal or if some service fails
	if err := runner.Run(ctx); err != nil {
		zap.L().Error("failed to run tasks", zap.Error(err))
	}

	closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
	defer cancelCloseCtx()
	if config.ForceStop {
		cancelCloseCtx()
	}

	// Mark service draining if not already.
	// If service stats was previously changed via API, we don't want to override it.
	if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Draining)
	}

	if err := runner.Close(closeCtx); err != nil {
		zap.L().Error("failed to close tasks", zap.Error(err))
		success = false
	}

	return success
}

func cleanupLogger(logger *zap.Logger) func(context.Context) error {
	return func(context.Context) error {
		if err := logger.Sync(); err != nil {
			// We expect /dev/stdout and /dev/stderr to error, as they don't implement `sync`
			if !errors.Is(err, syscall.EINVAL) {
				return nil
			}
		}
		return nil
	}
}
