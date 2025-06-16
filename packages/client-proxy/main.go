package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/proxy/internal"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	e2bproxy "github.com/e2b-dev/infra/packages/proxy/internal/proxy"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	e2bLogger "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	serviceName = "client-proxy"

	shutdownDrainingWait  = 30 * time.Second
	shutdownUnhealthyWait = 30 * time.Second

	version = "1.0.0"
)

var (
	commitSHA string

	useProxyCatalogResolution = os.Getenv("USE_PROXY_CATALOG_RESOLUTION") == "true"
)

func run() int {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	instanceID := uuid.New().String()

	// Setup telemetry
	var tel *telemetry.Client
	if env.IsLocal() {
		tel = telemetry.NewNoopClient()
	} else {
		var err error
		tel, err = telemetry.New(ctx, serviceName, commitSHA, instanceID)
		if err != nil {
			zap.L().Fatal("failed to create metrics exporter", zap.Error(err))
		}
	}

	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	logger := zap.Must(
		e2bLogger.NewLogger(
			ctx, e2bLogger.LoggerConfig{
				ServiceName:   serviceName,
				IsInternal:    true,Add commentMore actions
				IsDebug:       env.IsDebug(),
				Cores:         []zapcore.Core{e2bLogger.GetOTELCore(tel.LogsProvider, serviceName)},
				EnableConsole: true,
			},
		),
	)

	defer func() {
		err := logger.Sync()
		if err != nil {
			log.Printf("logger sync error: %v\n", err)
		}
	}()

	zap.ReplaceGlobals(logger)

	proxyPort := internal.GetProxyServicePort()
	edgePort := internal.GetEdgeServicePort()
	edgeSecret := internal.GetEdgeServiceSecret()
	orchestratorPort := internal.GetOrchestratorServicePort()

	exitCode := atomic.Int32{}

	wg := sync.WaitGroup{}

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	logger.Info("Starting client proxy", zap.String("commit", commitSHA), zap.String("instance_id", instanceID))

	tracer := tel.TracerProvider.Tracer(serviceName)

	edgeSD, orchestratorsSD, err := service_discovery.NewServiceDiscoveryProvider(ctx, edgePort, orchestratorPort, logger)
	if err != nil {
		logger.Error("failed to resolve service discovery config", zap.Error(err))
		return 1
	}

	var redisClient *redis.Client
	var catalog sandboxes.SandboxesCatalog

	redisUrl := os.Getenv("REDIS_URL")
	if redisUrl == "" {
		logger.Warn("Redis URL environment variable is not set, will fallback to in-memory sandboxes catalog that works only with one instance setup")
		catalog = sandboxes.NewMemorySandboxesCatalog(ctx, tracer)
	} else {
		opts, err := redis.ParseURL(redisUrl)
		if err != nil {
			zap.L().Fatal("invalid redis URL", zap.String("url", redisUrl), zap.Error(err))
		}

		redisClient = redis.NewClient(opts)
		redisSync := redsync.New(goredis.NewPool(redisClient))
		catalog = sandboxes.NewRedisSandboxesCatalog(ctx, tracer, redisClient, redisSync)
	}

	orchestrators := e2borchestrators.NewOrchestratorsPool(ctx, logger, orchestratorsSD, tracer)

	if !useProxyCatalogResolution {
		logger.Warn("Skipping proxy catalog resolution, using just DNS resolution instead. This is not recommended for production use, as it may lead to issues with sandbox resolution.")
	}

	// Proxy request to the correct node
	proxy, err := e2bproxy.NewClientProxy(tel.MeterProvider, serviceName, uint(proxyPort), catalog, orchestrators, useProxyCatalogResolution)
	if err != nil {
		logger.Error("failed to create client proxy", zap.Error(err))
		return 1
	}

	edgeApiStore, err := edge.NewEdgeAPIStore(ctx, logger, tracer, commitSHA, version, edgeSD, orchestrators, catalog)
	if err != nil {
		logger.Error("failed to create edge api store", zap.Error(err))
		return 1
	}

	edgeApiSwagger, err := api.GetSwagger()
	if err != nil {
		logger.Error("failed to get swagger", zap.Error(err))
		return 1
	}

	// Edge API server
	edgeGinServer := edge.NewGinServer(ctx, logger, edgeApiStore, edgeApiSwagger, tracer, edgePort, edgeSecret)

	wg.Add(1)
	go func() {
		defer wg.Done()

		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer sigCancel()

		edgeRunLogger := logger.With(zap.Int("edge_port", edgePort))
		edgeRunLogger.Info("edge http service starting")

		err := edgeGinServer.ListenAndServe()
		if err != nil {
			switch {
			case errors.Is(err, http.ErrServerClosed):
				edgeRunLogger.Info("edge http service shutdown successfully")
			case err != nil:
				exitCode.Add(1)
				edgeRunLogger.Error("edge http service encountered error", zap.Error(err))
			default:
				edgeRunLogger.Info("edge http service exited without error")
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer sigCancel()

		proxyRunLogger := logger.With(zap.Int("proxy_port", proxyPort))
		proxyRunLogger.Info("proxy http service starting")

		err := proxy.ListenAndServe()
		// Add different handling for the error
		switch {
		case errors.Is(err, http.ErrServerClosed):
			proxyRunLogger.Info("http service shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			proxyRunLogger.Error("http service encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			proxyRunLogger.Error("http service exited without error")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-signalCtx.Done()

		shutdownLogger := logger.With(zap.Int("proxy_port", proxyPort), zap.Int("edge_port", edgePort))
		shutdownLogger.Info("shutting down services")

		edgeApiStore.SetDraining()

		// we should wait for health check manager to notice that we are not ready for new traffic
		if !env.IsDevelopment() {
			shutdownLogger.Info("waiting for draining state propagation", zap.Float64("wait_in_seconds", shutdownDrainingWait.Seconds()))
			time.Sleep(shutdownDrainingWait)
		}

		proxyShutdownCtx, proxyShutdownCtxCancel := context.WithTimeout(ctx, 24*time.Hour)
		defer proxyShutdownCtxCancel()

		// gracefully shutdown the proxy http server
		err := proxy.Shutdown(proxyShutdownCtx)
		if err != nil {
			exitCode.Add(1)
			shutdownLogger.Error("proxy http service shutdown error", zap.Error(err))
		} else {
			shutdownLogger.Info("proxy http service shutdown successfully")
		}

		edgeApiStore.SetUnhealthy()

		// wait for the health check manager to notice that we are not healthy at all
		if !env.IsDevelopment() {
			shutdownLogger.Info("waiting for unhealthy state propagation", zap.Float64("wait_in_seconds", shutdownUnhealthyWait.Seconds()))
			time.Sleep(shutdownUnhealthyWait)
		}

		ginErr := edgeGinServer.Shutdown(ctx)
		if ginErr != nil {
			exitCode.Add(1)
			shutdownLogger.Error("edge http service shutdown error", zap.Error(ginErr))
		}
	}()

	wg.Wait()

	return int(exitCode.Load())
}

func main() {
	// Exit, with appropriate code.
	os.Exit(run())
}
