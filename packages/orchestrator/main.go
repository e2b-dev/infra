package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	clickhouseevents "github.com/e2b-dev/infra/packages/clickhouse/pkg/events"
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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service/machineinfo"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/volumes"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	event "github.com/e2b-dev/infra/packages/shared/pkg/events"
	sharedFactories "github.com/e2b-dev/infra/packages/shared/pkg/factories"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type closer struct {
	name  string
	close func(ctx context.Context) error
}

const version = "0.1.0"

var commitSHA string

func main() {
	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	if err = ensureDirs(config); err != nil {
		log.Fatalf("failed to create dirs: %v", err)
	}

	success := run(config)

	log.Println("Stopping orchestrator, success:", success)

	if success == false {
		os.Exit(1)
	}
}

func ensureDirs(c cfg.Config) error {
	for _, dir := range []string{
		c.DefaultCacheDir,
		c.OrchestratorBaseDir,
		c.StorageConfig.SandboxCacheDir,
		c.SandboxDir,
		c.SharedChunkCacheDir,
		c.StorageConfig.SnapshotCacheDir,
		c.StorageConfig.TemplateCacheDir,
		c.TemplatesDir,
	} {
		if dir == "" {
			continue
		}

		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to make %q: %w", dir, err)
		}
	}

	return nil
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

	sig, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	defer sigCancel()

	nodeID := env.GetNodeID()
	serviceName := cfg.GetServiceName(services)
	serviceInstanceID := uuid.NewString()

	// Detect CPU platform for orchestrator pool matching
	machineInfo, err := machineinfo.Detect()
	if err != nil {
		log.Printf("failed to detect machine info: %v", err)

		return false
	}

	serviceInfo := service.NewInfoContainer(ctx, nodeID, version, commitSHA, serviceInstanceID, machineInfo, config)

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
	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, version, serviceInstanceID)
	if err != nil {
		logger.L().Fatal(ctx, "failed to init telemetry", zap.Error(err))
	}
	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("error while shutting down telemetry: %v", err)
			success = false
		}
	}()

	globalLogger := utils.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
	}))
	defer func(l logger.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down logger: %v", err)
			success = false
		}
	}(globalLogger)
	logger.ReplaceGlobals(ctx, globalLogger)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer func(l logger.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down sandbox logger: %v", err)
			success = false
		}
	}(sbxLoggerExternal)
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       true,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer func(l logger.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down sandbox logger: %v", err)
			success = false
		}
	}(sbxLoggerInternal)
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	globalLogger.Info(ctx, "Starting orchestrator", zap.String("version", version), zap.String("commit", commitSHA), logger.WithServiceInstanceID(serviceInstanceID))

	startService := func(name string, f func() error) {
		g.Go(func() error {
			l := globalLogger.With(zap.String("service", name))
			l.Info(ctx, "starting service")

			err := f()
			if err != nil {
				l.Error(ctx, "service returned an error", zap.Error(err))
			}

			select {
			case serviceError <- err:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return serviceDoneError{name: name}
		})
	}

	var closers []closer

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := sandbox.NewSandboxesMap()

	// feature flags
	featureFlags, err := featureflags.NewClient()
	if err != nil {
		logger.L().Fatal(ctx, "failed to create feature flags client", zap.Error(err))
	}
	closers = append(closers, closer{"feature flags", featureFlags.Close})

	if config.DomainName != "" {
		featureFlags.SetDeploymentName(config.DomainName)
	}

	// gcp concurrent upload limiter
	limiter, err := limit.New(ctx, featureFlags)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create limiter", zap.Error(err))
	}
	closers = append(closers, closer{"limiter", limiter.Close})

	persistence, err := storage.GetTemplateStorageProvider(ctx, limiter)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create template storage provider", zap.Error(err))
	}

	blockMetrics, err := blockmetrics.NewMetrics(tel.MeterProvider)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create metrics provider", zap.Error(err))
	}

	templateCache, err := template.NewCache(config, featureFlags, persistence, blockMetrics)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create template cache", zap.Error(err))
	}
	templateCache.Start(ctx)
	closers = append(closers, closer{"template cache", func(context.Context) error {
		templateCache.Stop()

		return nil
	}})

	sbxEventsDeliveryTargets := make([]event.Delivery[event.SandboxEvent], 0)

	// Clickhouse sandbox events delivery target
	if config.ClickhouseConnectionString != "" {
		clickhouseConn, err := clickhouse.NewDriver(config.ClickhouseConnectionString)
		if err != nil {
			logger.L().Fatal(ctx, "failed to create clickhouse driver", zap.Error(err))
		}

		sbxEventsDeliveryClickhouse, err := clickhouseevents.NewDefaultClickhouseSandboxEventsDelivery(ctx, clickhouseConn, featureFlags)
		if err != nil {
			logger.L().Fatal(ctx, "failed to create clickhouse events delivery", zap.Error(err))
		}

		sbxEventsDeliveryTargets = append(sbxEventsDeliveryTargets, sbxEventsDeliveryClickhouse)
		closers = append(closers, closer{"sandbox events delivery for clickhouse", sbxEventsDeliveryClickhouse.Close})
	}

	// redis
	redisClient, err := sharedFactories.NewRedisClient(ctx, sharedFactories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
	})
	if err != nil && !errors.Is(err, sharedFactories.ErrRedisDisabled) {
		logger.L().Fatal(ctx, "Could not connect to Redis", zap.Error(err))
	} else if err == nil {
		closers = append(closers, closer{"redis client", func(context.Context) error {
			return sharedFactories.CloseCleanly(redisClient)
		}})
	}

	// Redis sandbox events delivery target
	if redisClient != nil {
		sbxEventsDeliveryRedis := event.NewRedisStreamsDelivery[event.SandboxEvent](redisClient, event.SandboxEventsStreamName)
		sbxEventsDeliveryTargets = append(sbxEventsDeliveryTargets, sbxEventsDeliveryRedis)
		closers = append(closers, closer{"sandbox events delivery for redis", sbxEventsDeliveryRedis.Close})
	}

	// sandbox observer
	sandboxObserver, err := metrics.NewSandboxObserver(ctx, nodeID, serviceName, commitSHA, version, serviceInstanceID, sandboxes)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create sandbox observer", zap.Error(err))
	}
	closers = append(closers, closer{"sandbox observer", sandboxObserver.Close})

	// sandbox proxy
	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, config.ProxyPort, sandboxes)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create sandbox proxy", zap.Error(err))
	}
	startService("sandbox proxy", func() error {
		err := sandboxProxy.Start(ctx)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	})
	closers = append(closers, closer{"sandbox proxy", sandboxProxy.Close})

	// hostname egress filter proxy
	tcpFirewall := tcpfirewall.New(
		globalLogger,
		config.NetworkConfig,
		sandboxes,
		tel.MeterProvider,
		featureFlags,
	)
	startService("tcp egress firewall", func() error {
		return tcpFirewall.Start(ctx)
	})
	closers = append(closers, closer{"tcp egress firewall", tcpFirewall.Close})

	// device pool
	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		logger.L().Fatal(ctx, "failed to create device pool", zap.Error(err))
	}
	startService("nbd device pool", func() error {
		devicePool.Populate(ctx)

		return nil
	})
	closers = append(closers, closer{"device pool", devicePool.Close})

	// network pool
	slotStorage, err := newStorage(ctx, nodeID, config.NetworkConfig)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create network pool", zap.Error(err))
	}
	networkPool := network.NewPool(network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, slotStorage, config.NetworkConfig)
	startService("network pool", func() error {
		networkPool.Populate(ctx)

		return nil
	})
	closers = append(closers, closer{"network pool", networkPool.Close})

	// sandbox factory
	sandboxFactory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, featureFlags)

	volumeService := volumes.New(config)

	orchestratorService := server.New(ctx, server.ServiceConfig{
		Config:           config,
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
		SbxEventsService: events.NewEventsService(sbxEventsDeliveryTargets),
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
	closers = append(closers, closer{
		"template manager sandbox logger", func(context.Context) error {
			// Sync returns EINVAL when path is /dev/stdout (for example)
			if err := tmplSbxLoggerExternal.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
				return err
			}

			return nil
		},
	})

	// hyperloop server
	hyperloopSrv, err := hyperloopserver.NewHyperloopServer(ctx, config.NetworkConfig.HyperloopProxyPort, globalLogger, sandboxes)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create hyperloop server", zap.Error(err))
	}
	startService("hyperloop server", func() error {
		err := hyperloopSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	})
	closers = append(closers, closer{"hyperloop server", hyperloopSrv.Shutdown})

	grpcServer := factories.NewGRPCServer(tel)
	orchestrator.RegisterSandboxServiceServer(grpcServer, orchestratorService)
	orchestrator.RegisterVolumeServiceServer(grpcServer, volumeService)

	// template manager
	var tmpl *tmplserver.ServerStore
	if slices.Contains(services, cfg.TemplateManager) {
		tmpl, err = tmplserver.New(
			ctx,
			config,
			featureFlags,
			tel.MeterProvider,
			globalLogger,
			tmplSbxLoggerExternal,
			sandboxFactory,
			sandboxProxy,
			sandboxes,
			templateCache,
			persistence,
			limiter,
		)
		if err != nil {
			logger.L().Fatal(ctx, "failed to create template manager", zap.Error(err))
		}

		templatemanager.RegisterTemplateServiceServer(grpcServer, tmpl)

		closers = append(closers, closer{"template server", tmpl.Close})
	}

	infoService := service.NewInfoService(serviceInfo, sandboxes)
	orchestratorinfo.RegisterInfoServiceServer(grpcServer, infoService)

	grpcHealth := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealth)

	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(ctx, config.GRPCPort)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create cmux server", zap.Error(err))
	}

	// Create all matchers BEFORE starting Serve() to avoid data race.
	// cmux.Match() modifies internal state that Serve() reads from.
	httpListener := cmuxServer.Match(cmux.HTTP1Fast())
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests

	startService("cmux server", func() error {
		logger.L().Info(ctx, "Starting network server", zap.Uint16("port", config.GRPCPort))
		err := cmuxServer.Serve()
		if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
			return nil
		}

		return err
	})
	closers = append(closers, closer{"cmux server", func(context.Context) error {
		logger.L().Info(ctx, "Shutting down cmux server")
		cmuxServer.Close()

		return nil
	}})

	// http server
	healthcheck, err := e2bhealthcheck.NewHealthcheck(serviceInfo)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create healthcheck", zap.Error(err))
	}

	httpServer := factories.NewHTTPServer()
	httpServer.Handler = healthcheck.CreateHandler()

	startService("http server", func() error {
		err := httpServer.Serve(httpListener)
		switch {
		case errors.Is(err, cmux.ErrServerClosed):
			return nil
		case errors.Is(err, http.ErrServerClosed):
			return nil
		default:
			return err
		}
	})
	closers = append(closers, closer{"http server", httpServer.Shutdown})

	// grpc server
	startService("grpc server", func() error {
		return grpcServer.Serve(grpcListener)
	})
	closers = append(closers, closer{"grpc server", func(context.Context) error {
		logger.L().Info(ctx, "Shutting down grpc server")
		grpcServer.GracefulStop()

		return nil
	}})

	// Wait for the shutdown signal or if some service fails
	select {
	case <-sig.Done():
		logger.L().Info(ctx, "Shutdown signal received")
	case serviceErr := <-serviceError:
		logger.L().Error(ctx, "Service error", zap.Error(serviceErr))
	}

	closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
	defer cancelCloseCtx()
	if config.ForceStop {
		cancelCloseCtx()
	}

	// Mark service draining if not already.
	// If service stats was previously changed via API, we don't want to override it.
	logger.L().Info(ctx, "Starting drain phase", zap.Int("sandbox_count", sandboxes.Count()))
	if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(ctx, orchestratorinfo.ServiceInfoStatus_Draining)

		// Wait for draining state to propagate to all consumers
		if !env.IsLocal() {
			time.Sleep(15 * time.Second)
		}
	}

	// Wait for services to be drained before closing them
	if tmpl != nil {
		err := tmpl.Wait(closeCtx)
		if err != nil {
			logger.L().Error(ctx, "error while waiting for template manager to drain", zap.Error(err))
			success = false
		}
	}

	slices.Reverse(closers)
	for _, closer := range closers {
		clog := globalLogger.With(zap.String("service", closer.name), zap.Bool("forced", config.ForceStop))
		clog.Info(ctx, "closing")
		if err := closer.close(closeCtx); err != nil {
			clog.Error(ctx, "error during shutdown", zap.Error(err))
			success = false
		}
	}

	logger.L().Info(ctx, "Waiting for services to finish")
	var sde serviceDoneError
	if err := g.Wait(); err != nil && !errors.As(err, &sde) {
		logger.L().Error(ctx, "service group error", zap.Error(err))
		success = false
	}

	return success
}

type serviceDoneError struct {
	name string
}

func (e serviceDoneError) Error() string {
	return fmt.Sprintf("service %s finished", e.name)
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func newStorage(ctx context.Context, nodeID string, config network.Config) (network.Storage, error) {
	if env.IsDevelopment() || config.UseLocalNamespaceStorage {
		return network.NewStorageLocal(ctx, config)
	}

	return network.NewStorageKV(nodeID, config)
}
