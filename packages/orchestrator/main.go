package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
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
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/pubsub"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Closeable interface {
	Close(ctx context.Context) error
}

const (
	defaultPort      = 5008
	defaultProxyPort = 5007

	version = "0.1.0"
)

var (
	forceStop    = env.GetEnv("FORCE_STOP", "false") == "true"
	commitSHA    string
	fileLockName = "/orchestrator.lock"
)

func init() {
	if value := os.Getenv("ORCHESTRATOR_LOCK_PATH"); value != "" {
		fileLockName = value
	}
}

func main() {
	port := flag.Uint("port", defaultPort, "orchestrator server port")
	proxyPort := flag.Uint("proxy-port", defaultProxyPort, "orchestrator proxy port")

	flag.Parse()

	if *port > math.MaxUint16 {
		log.Fatalf("%d is larger than maximum possible port %d", port, math.MaxInt16)
	}

	if *proxyPort > math.MaxUint16 {
		log.Fatalf("%d is larger than maximum possible proxy port %d", proxyPort, math.MaxInt16)
	}

	hyperloopPort, err := strconv.ParseInt(internal.GetHyperloopProxyPort(), 10, 32)
	if err != nil {
		log.Fatalf("failed to get hyperloop proxy port: %v", err)
	}

	success := run(*port, *proxyPort, uint(hyperloopPort))

	log.Println("Stopping orchestrator, success:", success)

	if success == false {
		os.Exit(1)
	}
}

func run(port, proxyPort, hyperloopPort uint) (success bool) {
	success = true

	services := service.GetServices()

	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	// We don't want to lock if the service is running with force stop; the subsequent start would fail.
	if !env.IsDevelopment() && !forceStop && slices.Contains(services, service.Orchestrator) {
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

	sig, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	nodeID := env.GetNodeID()
	serviceName := service.GetServiceName(services)
	serviceInstanceID := uuid.NewString()
	serviceInfo := service.NewInfoContainer(nodeID, version, commitSHA, serviceInstanceID)

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
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down logger: %v", err)
			success = false
		}
	}(globalLogger)
	zap.ReplaceGlobals(globalLogger)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer func(l *zap.Logger) {
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
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down sandbox logger: %v", err)
			success = false
		}
	}(sbxLoggerInternal)
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	globalLogger.Info("Starting orchestrator", zap.String("version", version), zap.String("commit", commitSHA), logger.WithServiceInstanceID(serviceInstanceID))

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, proxyPort, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox proxy", zap.Error(err))
	}

	networkPool, err := network.NewPool(ctx, tel.MeterProvider, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, nodeID)
	if err != nil {
		zap.L().Fatal("failed to create network pool", zap.Error(err))
	}

	devicePool, err := nbd.NewDevicePool(ctx, tel.MeterProvider)
	if err != nil {
		zap.L().Fatal("failed to create device pool", zap.Error(err))
	}

	grpcSrv := grpcserver.New(tel.TracerProvider, tel.MeterProvider, serviceInfo)

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		zap.L().Fatal("failed to create feature flags client", zap.Error(err))
	}

	limiter, err := limit.New(ctx, featureFlags)
	if err != nil {
		zap.L().Fatal("failed to create limiter", zap.Error(err))
	}

	persistence, err := storage.GetTemplateStorageProvider(ctx, limiter)
	if err != nil {
		zap.L().Fatal("failed to create template storage provider", zap.Error(err))
	}

	blockMetrics, err := blockmetrics.NewMetrics(tel.MeterProvider)
	if err != nil {
		zap.L().Fatal("failed to create metrics provider", zap.Error(err))
	}

	templateCache, err := template.NewCache(ctx, featureFlags, persistence, blockMetrics)
	if err != nil {
		zap.L().Fatal("failed to create template cache", zap.Error(err))
	}

	var sandboxEventBatcher batcher.ClickhouseInsertBatcher[clickhouse.SandboxEvent]

	clickhouseConnectionString := os.Getenv("CLICKHOUSE_CONNECTION_STRING")
	if clickhouseConnectionString == "" {
		sandboxEventBatcher = batcher.NewNoopBatcher[clickhouse.SandboxEvent]()
	} else {
		var err error
		clickhouseConn, err := clickhouse.NewDriver(clickhouseConnectionString)
		if err != nil {
			zap.L().Fatal("failed to create clickhouse driver", zap.Error(err))
		}

		maxBatchSize := 100
		if val, err := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxBatchSize); err == nil {
			maxBatchSize = val
		}

		maxDelay := 1 * time.Second
		if val, err := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxDelay); err == nil {
			maxDelay = time.Duration(val) * time.Millisecond
		}

		bactherQueueSize := 1000
		if val, err := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherQueueSize, featureflags.SandboxContext("clickhouse-batcher")); err == nil {
			bactherQueueSize = val
		}

		sandboxEventBatcher, err = batcher.NewSandboxEventInsertsBatcher(clickhouseConn, batcher.BatcherOptions{
			MaxBatchSize: maxBatchSize,
			MaxDelay:     maxDelay,
			QueueSize:    bactherQueueSize,
			ErrorHandler: func(err error) {
				zap.L().Error("error batching sandbox events", zap.Error(err))
			},
		})
		if err != nil {
			zap.L().Fatal("failed to create clickhouse batcher", zap.Error(err))
		}
	}

	var redisClient redis.UniversalClient
	if redisClusterUrl := os.Getenv("REDIS_CLUSTER_URL"); redisClusterUrl != "" {
		// For managed Redis Cluster in GCP we should use Cluster Client, because
		// > Redis node endpoints can change and can be recycled as nodes are added and removed over time.
		// https://cloud.google.com/memorystore/docs/cluster/cluster-node-specification#cluster_endpoints
		// https://cloud.google.com/memorystore/docs/cluster/client-library-code-samples#go-redis
		redisClient = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        []string{redisClusterUrl},
			MinIdleConns: 1,
		})
	} else if rurl := os.Getenv("REDIS_URL"); rurl != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:         rurl,
			MinIdleConns: 1,
		})
	} else {
		zap.L().Warn("REDIS_URL not set, using no-op pubsub")
	}

	if redisClient != nil {
		_, err := redisClient.Ping(ctx).Result()
		if err != nil {
			zap.L().Fatal("Could not connect to Redis", zap.Error(err))
		}

		zap.L().Info("Connected to Redis cluster")
	}

	var redisPubSub pubsub.PubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]
	if redisClient != nil {
		redisPubSub = pubsub.NewRedisPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData](redisClient, "sandbox-webhooks")
	} else {
		redisPubSub = pubsub.NewMockPubSub[event.SandboxEvent, webhooks.SandboxWebhooksMetaData]()
	}

	sbxEventsService := events.NewSandboxEventsService(featureFlags, redisPubSub, sandboxEventBatcher, globalLogger)
	sandboxObserver, err := metrics.NewSandboxObserver(ctx, nodeID, serviceName, commitSHA, version, serviceInstanceID, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox observer", zap.Error(err))
	}

	_, err = server.New(
		ctx,
		server.ServiceConfig{
			GRPC:             grpcSrv,
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
		},
	)
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceNameTemplate,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down template manager sandbox logger: %v", err)
			success = false
		}
	}(tmplSbxLoggerExternal)

	hyperloopSrv, err := hyperloopserver.NewHyperloopServer(ctx, hyperloopPort, globalLogger, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create hyperloop server", zap.Error(err))
	}

	var closers []Closeable
	closers = append(closers,
		grpcSrv,
		networkPool,
		devicePool,
		sandboxProxy,
		featureFlags,
		sandboxObserver,
		limiter,
		sandboxEventBatcher,
	)

	// Initialize the template manager only if the service is enabled
	if slices.Contains(services, service.TemplateManager) {
		tmpl, err := tmplserver.New(
			ctx,
			tel.MeterProvider,
			globalLogger,
			tmplSbxLoggerExternal,
			grpcSrv,
			networkPool,
			devicePool,
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

		closers = append([]Closeable{tmpl}, closers...)
	}

	service.NewInfoService(ctx, grpcSrv.GRPCServer(), serviceInfo, sandboxes)

	g.Go(func() error {
		zap.L().Info("Starting session proxy")
		proxyErr := sandboxProxy.Start()
		if proxyErr != nil && !errors.Is(proxyErr, http.ErrServerClosed) {
			proxyErr = fmt.Errorf("proxy server: %w", proxyErr)
			zap.L().Error("error starting proxy server", zap.Error(proxyErr))

			select {
			case serviceError <- proxyErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return proxyErr
		}

		return nil
	})

	g.Go(func() (err error) {
		// this sets the error declared above so the function
		// in the defer can check it.
		hyperloopErr := hyperloopSrv.ListenAndServe()
		if hyperloopErr != nil {
			hyperloopErr = fmt.Errorf("hyperloop server: %w", hyperloopErr)
			zap.L().Error("hyperloop server error", zap.Error(hyperloopErr))

			select {
			case serviceError <- hyperloopErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return hyperloopErr
		}

		return nil
	})

	g.Go(func() (err error) {
		// this sets the error declared above so the function
		// in the defer can check it.
		grpcErr := grpcSrv.Start(ctx, port)
		if grpcErr != nil {
			grpcErr = fmt.Errorf("grpc server: %w", grpcErr)
			zap.L().Error("grpc server error", zap.Error(grpcErr))

			select {
			case serviceError <- grpcErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return grpcErr
		}

		return nil
	})

	// Wait for the shutdown signal or if some service fails
	select {
	case <-sig.Done():
		zap.L().Info("Shutdown signal received")
	case serviceErr := <-serviceError:
		zap.L().Error("Service error", zap.Error(serviceErr))
	}

	closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
	defer cancelCloseCtx()
	if forceStop {
		cancelCloseCtx()
	}

	// Mark service draining if not already.
	// If service stats was previously changed via API, we don't want to override it.
	if serviceInfo.GetStatus() == orchestrator.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(orchestrator.ServiceInfoStatus_Draining)
	}

	for _, c := range closers {
		zap.L().Info(fmt.Sprintf("Closing %T, forced: %v", c, forceStop))
		if err := c.Close(closeCtx); err != nil {
			zap.L().Error("error during shutdown", zap.Error(err))
			success = false
		}
	}

	zap.L().Info("Shutting down hyperloop server")
	err = hyperloopSrv.Shutdown(closeCtx)
	if err != nil {
		zap.L().Error("error shutting down hyperloop server", zap.Error(err))
		success = false
	}

	zap.L().Info("Waiting for services to finish")
	if err := g.Wait(); err != nil {
		zap.L().Error("service group error", zap.Error(err))
		success = false
	}

	return success
}
