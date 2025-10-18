package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"slices"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type closer struct {
	name  string
	close func(ctx context.Context) error
}

const version = "0.1.0"

var commitSHA string

// HealthHTTPServer wraps the health check HTTP server to distinguish it from HyperloopHTTPServer in DI
type HealthHTTPServer struct {
	*http.Server
}

// HyperloopHTTPServer wraps the hyperloop HTTP server to distinguish it from HealthHTTPServer in DI
type HyperloopHTTPServer struct {
	*http.Server
}

func main() {
	fx.New(
		fx.Provide(
			NewConfig,
			NewState,
			NewTelemetry,
			NewGlobalLogger,
			NewFeatureFlagsClient,
			NewLimiter,
			NewPersistence,
			NewSandboxesMap,
			NewBlockMetrics,
			NewTemplateCache,
			NewSandboxEventBatcher,
			NewRedis,
			NewPubSub,
			NewSandboxEventsService,
			NewSandboxObserver,
			NewSandboxProxy,
			NewHyperloopServer,
			NewDevicePool,
			NewNetworkPool,
			NewSandboxFactory,
			NewOrchestratorService,
			NewServiceInfo,
			NewGRPCServer,
			NewInfoService,
			NewGRPCHealthServer,
			NewCMUXServer,
			NewHTTPServer,
			NewGRPCCMUXServer,
			NewTemplateManager,
		),
		fx.Invoke(
			NewSingleOrchestratorCheck,   // Lock file check for single orchestrator
			NewDrainingHandler,           // Graceful shutdown handler
			NewSandboxLoggerInternal,     // Initialize sandbox internal logger
			NewSandboxLoggerExternal,     // Initialize sandbox external logger
			func(HyperloopHTTPServer) {}, // Hyperloop HTTP server (independent)
			StartCMUXServer,              // Start CMUX (FX ensures this runs before HTTP/gRPC)
			func(HealthHTTPServer) {},    // Health HTTP server
			func(net.Listener) {},        // gRPC server
		),
	).Run()
}

func NewSingleOrchestratorCheck(
	lc fx.Lifecycle,
	config cfg.Config,
	state State,
	serviceInfo *service.ServiceInfo,
) {
	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	// We don't want to lock if the service is running with force stop; the subsequent start would fail.
	if !env.IsDevelopment() && !config.ForceStop && slices.Contains(state.Services, cfg.Orchestrator) {
		fileLockName := config.OrchestratorLockPath
		info, err := os.Stat(fileLockName)
		if err == nil {
			log.Fatalf("Orchestrator was already started at %s, exiting", info.ModTime())
		}

		f, err := os.Create(fileLockName)
		if err != nil {
			log.Fatalf("Failed to create lock file %s: %v", fileLockName, err)
		}
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				fileErr := f.Close()
				if fileErr != nil {
					log.Printf("Failed to close lock file %s: %v", fileLockName, fileErr)
				}

				// TODO: DO ONLY ON GRACEUL SHUTDOWN
				// Remove the lock file on graceful shutdown
				if fileErr = os.Remove(fileLockName); fileErr != nil {
					log.Printf("Failed to remove lock file %s: %v", fileLockName, fileErr)
				}
				return nil
			},
		})
	}
}

func NewDrainingHandler(
	lc fx.Lifecycle,
	serviceInfo *service.ServiceInfo,
) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			// Mark service draining if not already.
			// If service stats was previously changed via API, we don't want to override it.
			if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
				serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Draining)
			}
			return nil
		},
	})
}

func NewTemplateManager(
	lc fx.Lifecycle,
	sandboxFactory *sandbox.Factory,
	sandboxProxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	templateCache *template.Cache,
	persistence storage.StorageProvider,
	limiter *limit.Limiter,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
	state State,
	tel *telemetry.Client,
) *tmplserver.ServerStore {
	if !slices.Contains(state.Services, cfg.TemplateManager) {
		return nil
	}

	// template manager sandbox logger
	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceNameTemplate,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := tmplSbxLoggerExternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down template manager sandbox logger: %v", err)
				return err
			}
			return nil
		},
	})

	tmpl, err := tmplserver.New(
		context.Background(),
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
		globalLogger.Fatal("failed to create template manager", zap.Error(err))
	}

	globalLogger.Info("Registered gRPC service", zap.String("service", "template_manager.TemplateService"))

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down template manager")
			return tmpl.Close(ctx)
		},
	})

	return tmpl
}

func NewGRPCCMUXServer(
	lc fx.Lifecycle,
	grpcServer *grpc.Server,
	cmuxServer cmux.CMux,
	globalLogger *zap.Logger,
) net.Listener {
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting gRPC server to serve all registered services")
			go func() {
				err := grpcServer.Serve(grpcListener)
				if err != nil {
					globalLogger.Error("gRPC server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down grpc server")
			grpcServer.GracefulStop()
			return nil
		},
	})
	return grpcListener
}

func NewHTTPServer(
	lc fx.Lifecycle,
	cmuxServer cmux.CMux,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
) HealthHTTPServer {
	httpListener := cmuxServer.Match(cmux.HTTP1Fast())
	healthcheck, err := e2bhealthcheck.NewHealthcheck(serviceInfo)
	if err != nil {
		globalLogger.Fatal("failed to create healthcheck", zap.Error(err))
	}
	httpServer := factories.NewHTTPServer()
	httpServer.Handler = healthcheck.CreateHandler()

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				err := httpServer.Serve(httpListener)
				if err != nil && !errors.Is(err, cmux.ErrServerClosed) && !errors.Is(err, http.ErrServerClosed) {
					globalLogger.Error("HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down http server")
			return httpServer.Shutdown(ctx)
		},
	})

	return HealthHTTPServer{httpServer}
}

func NewCMUXServer(
	config cfg.Config,
	globalLogger *zap.Logger,
) cmux.CMux {
	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(context.Background(), config.GRPCPort)
	if err != nil {
		globalLogger.Fatal("failed to create cmux server", zap.Error(err))
	}

	return cmuxServer
}

// StartCMUXServer starts the CMUX server and must be invoked before HTTP/gRPC servers start
func StartCMUXServer(
	lc fx.Lifecycle,
	cmuxServer cmux.CMux,
	config cfg.Config,
	globalLogger *zap.Logger,
) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting network server", zap.Uint16("port", config.GRPCPort))
			go func() {
				err := cmuxServer.Serve()
				if err != nil {
					globalLogger.Error("CMUX server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down cmux server")
			cmuxServer.Close()
			return nil
		},
	})
}

func NewGRPCHealthServer(
	globalLogger *zap.Logger,
) *health.Server {
	s := health.NewServer()
	globalLogger.Info("Registered gRPC service", zap.String("service", "grpc.health.v1.Health"))
	return s
}

func NewInfoService(
	sandboxes *sandbox.Map,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
) *service.Server {
	s := service.NewInfoService(serviceInfo, sandboxes)
	globalLogger.Info("Registered gRPC service", zap.String("service", "orchestrator_info.InfoService"))
	return s
}

func NewGRPCServer(
	tel *telemetry.Client,
	orchestratorService *server.Server,
	globalLogger *zap.Logger,
	healthService *health.Server,
	infoService *service.Server,
	tmpl *tmplserver.ServerStore,
) *grpc.Server {
	s := factories.NewGRPCServer(tel)

	grpc_health_v1.RegisterHealthServer(s, healthService)
	orchestratorinfo.RegisterInfoServiceServer(s, infoService)
	orchestrator.RegisterSandboxServiceServer(s, orchestratorService)
	if tmpl != nil {
		templatemanager.RegisterTemplateServiceServer(s, tmpl)
	}

	globalLogger.Info("Registered gRPC service", zap.String("service", "orchestrator.SandboxService"))
	return s
}

func NewServiceInfo(state State, config cfg.Config) *service.ServiceInfo {
	nodeID := state.NodeID
	serviceInstanceID := state.ServiceInstanceID

	return service.NewInfoContainer(nodeID, version, commitSHA, serviceInstanceID, config)
}

func NewOrchestratorService(
	sandboxFactory *sandbox.Factory,
	tel *telemetry.Client,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	templateCache *template.Cache,
	sandboxProxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	persistence storage.StorageProvider,
	featureFlags *featureflags.Client,
	sbxEventsService *events.SandboxEventsService,
	serviceInfo *service.ServiceInfo,
) *server.Server {
	return server.New(server.ServiceConfig{
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
}

func NewSandboxFactory(
	config cfg.Config,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	featureFlags *featureflags.Client,
) *sandbox.Factory {
	return sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, featureFlags)
}

func NewNetworkPool(
	lc fx.Lifecycle,
	config cfg.Config,
	globalLogger *zap.Logger,
) (*network.Pool, error) {
	networkPool, err := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, env.GetNodeID(), config.NetworkConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting network pool")
			go networkPool.Populate(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down network pool")
			return networkPool.Close(ctx)
		},
	})

	return networkPool, nil
}

func NewDevicePool(
	lc fx.Lifecycle,
	globalLogger *zap.Logger,
) (*nbd.DevicePool, error) {
	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return nil, fmt.Errorf("failed to create device pool: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			globalLogger.Info("Starting NBD device pool")
			go devicePool.Populate(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down NBD device pool")
			return devicePool.Close(ctx)
		},
	})

	return devicePool, nil
}

func NewHyperloopServer(
	lc fx.Lifecycle,
	config cfg.Config,
	globalLogger *zap.Logger,
	sandboxes *sandbox.Map,
) (HyperloopHTTPServer, error) {
	hyperloopSrv, err := hyperloopserver.NewHyperloopServer(context.Background(), config.NetworkConfig.HyperloopProxyPort, globalLogger, sandboxes)
	if err != nil {
		return HyperloopHTTPServer{}, fmt.Errorf("failed to create hyperloop server: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				err := hyperloopSrv.ListenAndServe()
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					globalLogger.Error("Hyperloop server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return hyperloopSrv.Shutdown(ctx)
		},
	})

	return HyperloopHTTPServer{hyperloopSrv}, nil
}

func NewSandboxProxy(
	lc fx.Lifecycle,
	tel *telemetry.Client,
	config cfg.Config,
	sandboxes *sandbox.Map,
	globalLogger *zap.Logger,
) (*proxy.SandboxProxy, error) {
	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, config.ProxyPort, sandboxes)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox proxy: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				err := sandboxProxy.Start(ctx)
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					globalLogger.Error("Sandbox proxy error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			globalLogger.Info("Shutting down sandbox proxy")
			return sandboxProxy.Close(ctx)
		},
	})

	return sandboxProxy, nil
}

func NewSandboxObserver(
	lc fx.Lifecycle,
	state State,
	sandboxes *sandbox.Map,
	globalLogger *zap.Logger,
) (*metrics.SandboxObserver, error) {
	sandboxObserver, err := metrics.NewSandboxObserver(context.Background(), state.NodeID, state.ServiceName, commitSHA, version, state.ServiceInstanceID, sandboxes)
	if err != nil {
		globalLogger.Fatal("failed to create sandbox observer", zap.Error(err))
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return sandboxObserver.Close(ctx)
		},
	})

	return sandboxObserver, nil
}

func NewSandboxEventsService(
	featureFlags *featureflags.Client,
	redisPubSub pubsub.PubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData],
	sandboxEventBatcher batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent],
	globalLogger *zap.Logger,
) *events.SandboxEventsService {
	return events.NewSandboxEventsService(featureFlags, redisPubSub, sandboxEventBatcher, globalLogger)
}

func NewPubSub(
	lc fx.Lifecycle,
	redisClient redis.UniversalClient,
) (pubsub.PubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData], error) {
	var redisPubSub pubsub.PubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]
	if redisClient != nil {
		redisPubSub = pubsub.NewRedisPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData](redisClient, "sandbox-webhooks")
	} else {
		redisPubSub = pubsub.NewMockPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]()
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return redisPubSub.Close(ctx)
		},
	})

	return redisPubSub, nil
}

func NewRedis(
	lc fx.Lifecycle,
	config cfg.Config,
	globalLogger *zap.Logger,
) (redis.UniversalClient, error) {
	redisClient, err := factories.NewRedisClient(context.Background(), config)
	if err != nil && !errors.Is(err, factories.ErrRedisDisabled) {
		globalLogger.Fatal("Could not connect to Redis", zap.Error(err))
	} else if err == nil {
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return factories.CloseCleanly(redisClient)
			},
		})
	}

	return redisClient, nil
}

func NewSandboxEventBatcher(
	lc fx.Lifecycle,
	config cfg.Config,
	featureFlags *featureflags.Client,
	globalLogger *zap.Logger,
) (batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent], error) {
	var sandboxEventBatcher batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent]

	clickhouseConnectionString := config.ClickhouseConnectionString
	if clickhouseConnectionString == "" {
		sandboxEventBatcher = batcher.NewNoopBatcher[clickhouse.SandboxEvent]()
	} else {
		clickhouseConn, err := clickhouse.NewDriver(clickhouseConnectionString)
		if err != nil {
			globalLogger.Fatal("failed to create clickhouse driver", zap.Error(err))
		}

		sandboxEventBatcher, err = factories.NewSandboxInsertsEventBatcher(context.Background(), clickhouseConn, featureFlags)
		if err != nil {
			globalLogger.Fatal("failed to create clickhouse batcher", zap.Error(err))
		}
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return sandboxEventBatcher.Close(ctx)
		},
	})

	return sandboxEventBatcher, nil
}

func NewTemplateCache(
	config cfg.Config,
	featureFlags *featureflags.Client,
	persistence storage.StorageProvider,
	blockMetrics blockmetrics.Metrics,
) (*template.Cache, error) {
	return template.NewCache(context.Background(), config, featureFlags, persistence, blockMetrics)
}

func NewBlockMetrics(tel *telemetry.Client) (blockmetrics.Metrics, error) {
	return blockmetrics.NewMetrics(tel.MeterProvider)
}

func NewPersistence(limiter *limit.Limiter) (storage.StorageProvider, error) {
	return storage.GetTemplateStorageProvider(context.Background(), limiter)
}

func NewLimiter(lc fx.Lifecycle, featureFlags *featureflags.Client) (*limit.Limiter, error) {
	limiter, err := limit.New(context.Background(), featureFlags)
	if err != nil {
		return nil, fmt.Errorf("failed to create limiter: %w", err)
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return limiter.Close(ctx)
		},
	})

	return limiter, nil
}

func NewFeatureFlagsClient(lc fx.Lifecycle) (*featureflags.Client, error) {
	ff, err := featureflags.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create feature flags client: %w", err)
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return ff.Close(ctx)
		},
	})

	return ff, nil
}

func NewSandboxesMap() *sandbox.Map {
	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	return sandbox.NewSandboxesMap()
}

func NewSandboxLoggerInternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
	sbxLoggerInternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      state.ServiceName,
			IsInternal:       true,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := sbxLoggerInternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down sandbox internal logger: %v", err)
				return err
			}

			return nil
		},
	})
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)
}

func NewSandboxLoggerExternal(lc fx.Lifecycle, tel *telemetry.Client, state State) {
	sbxLoggerExternal := sbxlogger.NewLogger(
		context.Background(),
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      state.ServiceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := sbxLoggerExternal.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down sandbox external logger: %v", err)
				return err
			}

			return nil
		},
	})
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)
}

func NewGlobalLogger(lc fx.Lifecycle, tel *telemetry.Client, state State) *zap.Logger {
	globalLogger := zap.Must(logger.NewLogger(context.Background(), logger.LoggerConfig{
		ServiceName:   state.ServiceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, state.ServiceName)},
		EnableConsole: true,
	}))
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := globalLogger.Sync()
			if logger.IsSyncError(err) {
				log.Printf("error while shutting down logger: %v", err)
				return err
			}

			return nil
		},
	})
	zap.ReplaceGlobals(globalLogger)

	globalLogger.Info("Starting orchestrator", zap.String("version", version), zap.String("commit", commitSHA), logger.WithServiceInstanceID(state.ServiceInstanceID))

	return globalLogger
}

type State struct {
	Services          []cfg.ServiceType
	NodeID            string
	ServiceName       string
	ServiceInstanceID string
}

func NewState(lc fx.Lifecycle, config cfg.Config) State {
	services := cfg.GetServices(config)
	nodeID := env.GetNodeID()
	serviceName := cfg.GetServiceName(services)
	serviceInstanceID := uuid.NewString()

	return State{
		Services:          services,
		NodeID:            nodeID,
		ServiceName:       serviceName,
		ServiceInstanceID: serviceInstanceID,
	}
}

func NewConfig(lc fx.Lifecycle) cfg.Config {
	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	return config
}

func NewTelemetry(lc fx.Lifecycle, config cfg.Config, state State) *telemetry.Client {
	// Setup telemetry
	tel, err := telemetry.New(context.Background(), state.NodeID, state.ServiceName, commitSHA, version, state.ServiceInstanceID)
	if err != nil {
		zap.L().Fatal("failed to init telemetry", zap.Error(err))
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err := tel.Shutdown(ctx)
			if err != nil {
				log.Printf("error while shutting down telemetry: %v", err)
				return err
			}
			return nil
		},
	})

	return tel
}
