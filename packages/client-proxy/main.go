package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net"
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
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/proxy/internal"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge-pass-through"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/authorization"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	e2bproxy "github.com/e2b-dev/infra/packages/proxy/internal/proxy"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	e2bLogger "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Closeable interface {
	Close(context.Context) error
}

const (
	serviceName = "client-proxy"

	shutdownDrainingWait  = 15 * time.Second
	shutdownUnhealthyWait = 15 * time.Second

	version = "1.0.0"
)

var (
	commitSHA string

	useProxyCatalogResolution = os.Getenv("USE_CATALOG_RESOLUTION") == "true"
	useDnsResolution          = os.Getenv("USE_DNS_RESOLUTION") != "false"
)

func run() int {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	instanceID := uuid.New().String()

	// Setup telemetry
	var tel *telemetry.Client
	if telemetry.OtelCollectorGRPCEndpoint == "" {
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
				IsInternal:    true,
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
		logger.Error("Failed to resolve service discovery config", zap.Error(err))
		return 1
	}

	var catalog sandboxes.SandboxesCatalog

	if redisClusterUrl := os.Getenv("REDIS_CLUSTER_URL"); redisClusterUrl != "" {
		redisClient := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{redisClusterUrl}, MinIdleConns: 1})
		redisSync := redsync.New(goredis.NewPool(redisClient))
		catalog = sandboxes.NewRedisSandboxesCatalog(tracer, redisClient, redisSync)
	} else if redisUrl := os.Getenv("REDIS_URL"); redisUrl != "" {
		redisClient := redis.NewClient(&redis.Options{Addr: redisUrl, MinIdleConns: 1})
		redisSync := redsync.New(goredis.NewPool(redisClient))
		catalog = sandboxes.NewRedisSandboxesCatalog(tracer, redisClient, redisSync)
	} else {
		logger.Warn("Redis environment variable is not set, will fallback to in-memory sandboxes catalog that works only with one instance setup")
		catalog = sandboxes.NewMemorySandboxesCatalog(tracer)
	}

	orchestrators := e2borchestrators.NewOrchestratorsPool(logger, tracer, tel.TracerProvider, tel.MeterProvider, orchestratorsSD)

	info := &e2binfo.ServiceInfo{
		NodeID:               internal.GetNodeID(),
		ServiceInstanceID:    uuid.NewString(),
		ServiceVersion:       version,
		ServiceVersionCommit: commitSHA,
		ServiceStartup:       time.Now(),
		Host:                 fmt.Sprintf("%s:%d", internal.GetNodeIP(), edgePort),
	}

	// service starts in unhealthy state, and we are waiting for initial health check to pass
	info.SetStatus(api.Unhealthy)

	if !useProxyCatalogResolution {
		logger.Warn("Skipping proxy catalog resolution, using just DNS resolution instead. This is not recommended for production use, as it may lead to issues with sandbox resolution.")
	}

	// Proxy sandbox http traffic to orchestrator nodes
	trafficProxy, err := e2bproxy.NewClientProxy(tel.MeterProvider, serviceName, uint(proxyPort), catalog, orchestrators, useProxyCatalogResolution, useDnsResolution)
	if err != nil {
		logger.Error("Failed to create client proxy", zap.Error(err))
		return 1
	}

	authorizationManager := authorization.NewStaticTokenAuthorizationService(edgeSecret)
	edges := e2borchestrators.NewEdgePool(logger, edgeSD, tracer, info.Host, authorizationManager)

	var closers []Closeable
	closers = append(closers, orchestrators, edges)

	edgeApiStore, err := edge.NewEdgeAPIStore(ctx, logger, tracer, info, edges, orchestrators, catalog)
	if err != nil {
		logger.Error("failed to create edge api store", zap.Error(err))
		return 1
	}

	edgeApiSwagger, err := api.GetSwagger()
	if err != nil {
		logger.Error("Failed to get swagger", zap.Error(err))
		return 1
	}

	lisAddr := fmt.Sprintf("0.0.0.0:%d", edgePort)
	lis, err := net.Listen("tcp", lisAddr)
	if err != nil {
		logger.Error("Failed to listen on edge port", zap.Int("port", edgePort), zap.Error(err))
		return 1
	}

	muxServer := cmux.New(lis)

	// Edge Pass Through Proxy for direct communication with orchestrator nodes
	grpcListener := muxServer.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc")) // handler requests for gRPC pass through
	grpcSrv := edgepassthrough.NewNodePassThroughServer(orchestrators, info, authorizationManager, catalog)

	// Edge REST API
	restHttpHandler := edge.NewGinServer(logger, edgeApiStore, edgeApiSwagger, tracer, authorizationManager)
	restListener := muxServer.Match(cmux.Any())
	restSrv := &http.Server{Handler: restHttpHandler} // handler requests for REST API

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := grpcSrv.Serve(grpcListener)
		switch {
		case errors.Is(err, http.ErrServerClosed):
			zap.L().Info("Edge grpc service shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			zap.L().Error("Edge grpc service encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			zap.L().Error("Edge grpc service exited without error")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := restSrv.Serve(restListener)
		switch {
		case errors.Is(err, http.ErrServerClosed):
			zap.L().Info("Edge api service shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			zap.L().Error("Edge api service encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			zap.L().Error("Edge api service exited without error")
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

		edgeRunLogger := logger.With(zap.Int("edge_port", edgePort))
		edgeRunLogger.Info("Edge api starting")

		err := muxServer.Serve()
		if err != nil {
			switch {
			case errors.Is(err, http.ErrServerClosed):
				edgeRunLogger.Info("Edge api shutdown successfully")
			case err != nil:
				exitCode.Add(1)
				edgeRunLogger.Error("Edge api encountered error", zap.Error(err))
			default:
				edgeRunLogger.Info("Edge api exited without error")
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
		proxyRunLogger.Info("Http proxy starting")

		err := trafficProxy.ListenAndServe()
		// Add different handling for the error
		switch {
		case errors.Is(err, http.ErrServerClosed):
			proxyRunLogger.Info("Http proxy shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			proxyRunLogger.Error("Http proxy encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			proxyRunLogger.Error("Http proxy exited without error")
		}
	}()

	// Service gracefully shutdown flow
	//
	// Endpoints reporting health status for different consumers
	// -> Edge API and GRPC proxy /health
	// -> Sandbox traffic proxy   /health/traffic
	// -> Edge machine            /health/machine
	//
	// When service shut-downs we need to info all services that depends on us gracefully shutting down existing connections.
	// Shutdown phase starts with marking sandbox traffic as draining.
	// After that we will wait some time so all dependent services will recognize that we are draining and will stop sending new requests.
	// Following phase marks the service as unhealthy, we are waiting for some time to let dependent services recognize new state.
	// After that we are shutting down the GRPC proxy and edge API servers. This can take some time,
	// because we are waiting for all in-progress requests to finish.
	// After GRPC proxy and edge API servers are gracefully shutdown, we are marking the service as terminating,
	// this is message primary for instances management that we are ready to be terminated and everything is properly cleaned up.
	// Finally, we are closing the mux server just to clean up, new connections will not be accepted anymore because we already closed listeners.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-signalCtx.Done()

		shutdownLogger := logger.With(zap.Int("proxy_port", proxyPort), zap.Int("edge_port", edgePort))
		shutdownLogger.Info("Shutting down services")

		edgeApiStore.SetDraining()

		// we should wait for health check manager to notice that we are not ready for new traffic
		shutdownLogger.Info("Waiting for draining state propagation", zap.Float64("wait_in_seconds", shutdownDrainingWait.Seconds()))
		time.Sleep(shutdownDrainingWait)

		proxyShutdownCtx, proxyShutdownCtxCancel := context.WithTimeout(ctx, 24*time.Hour)
		defer proxyShutdownCtxCancel()

		// gracefully shutdown the proxy http server
		err := trafficProxy.Shutdown(proxyShutdownCtx)
		if err != nil {
			exitCode.Add(1)
			shutdownLogger.Error("Http proxy shutdown error", zap.Error(err))
		} else {
			shutdownLogger.Info("Http proxy shutdown successfully")
		}

		edgeApiStore.SetUnhealthy()

		// wait for the health check manager to notice that we are not healthy at all
		shutdownLogger.Info("Waiting for unhealthy state propagation", zap.Float64("wait_in_seconds", shutdownUnhealthyWait.Seconds()))
		time.Sleep(shutdownUnhealthyWait)

		// wait for graceful shutdown of the gRPC server with  pass through proxy
		// it can take some time, because we are waiting for all in-progress requests to finish (sandbox spawning, pausing...)
		grpcSrv.GracefulStop()

		// wait for graceful shutdown of the rest api server with health check
		restShutdownCtx, restShutdownCtxCancel := context.WithTimeout(ctx, shutdownDrainingWait)
		defer restShutdownCtxCancel()

		err = restSrv.Shutdown(restShutdownCtx)
		if err != nil {
			shutdownLogger.Error("Edge rest api shutdown error", zap.Error(err))
		}

		// used by instances management for notify that instance is ready for termination
		edgeApiStore.SetTerminating()

		// close the mux server
		muxServer.Close()

		closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
		defer cancelCloseCtx()

		// close all resources that needs to be closed gracefully
		for _, c := range closers {
			zap.L().Info(fmt.Sprintf("Closing %T", c))
			if err := c.Close(closeCtx); err != nil {
				zap.L().Error("error during shutdown", zap.Error(err))
			}
		}
	}()

	wg.Wait()

	return int(exitCode.Load())
}

func main() {
	// Exit, with appropriate code.
	os.Exit(run())
}
