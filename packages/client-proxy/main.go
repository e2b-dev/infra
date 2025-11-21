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

	"github.com/google/uuid"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge"
	edgepassthrough "github.com/e2b-dev/infra/packages/proxy/internal/edge-pass-through"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/authorization"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	e2bproxy "github.com/e2b-dev/infra/packages/proxy/internal/proxy"
	servicediscovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	feature_flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Closeable interface {
	Close(ctx context.Context) error
}

const (
	serviceName = "client-proxy"

	shutdownDrainingWait  = 15 * time.Second
	shutdownUnhealthyWait = 15 * time.Second

	version = "1.0.0"
)

var commitSHA string

func run() int {
	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v\n", err)
	}

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	instanceID := uuid.New().String()
	nodeID := env.GetNodeID()

	// Setup telemetry
	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, version, instanceID)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create metrics exporter", zap.Error(err))
	}
	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	l := utils.Must(
		logger.NewLogger(
			ctx, logger.LoggerConfig{
				ServiceName:   serviceName,
				IsInternal:    true,
				IsDebug:       env.IsDebug(),
				Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
				EnableConsole: true,
			},
		),
	)

	defer func() {
		err := l.Sync()
		if err != nil {
			log.Printf("logger sync error: %v\n", err)
		}
	}()
	logger.ReplaceGlobals(ctx, l)

	exitCode := atomic.Int32{}

	wg := sync.WaitGroup{}

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	l.Info(ctx, "Starting client proxy", zap.String("commit", commitSHA), zap.String("instance_id", instanceID))

	edgeSD, err := servicediscovery.BuildServiceDiscoveryProvider(ctx, config.EdgeServiceDiscovery, config.EdgePort, l)
	if err != nil {
		l.Error(ctx, "Failed to build edge discovery config", zap.Error(err))

		return 1
	}

	orchestratorsSD, err := servicediscovery.BuildServiceDiscoveryProvider(ctx, config.OrchestratorServiceDiscovery, config.OrchestratorPort, l)
	if err != nil {
		l.Error(ctx, "Failed to build orchestrator discovery config", zap.Error(err))

		return 1
	}

	featureFlagsClient, err := feature_flags.NewClient()
	if err != nil {
		l.Error(ctx, "Failed to create feature flags client", zap.Error(err))

		return 1
	}

	var catalog e2bcatalog.SandboxesCatalog

	redisClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: "",
	})
	if err == nil {
		defer func() {
			err := factories.CloseCleanly(redisClient)
			if err != nil {
				l.Error(ctx, "Failed to close redis client", zap.Error(err))
			}
		}()
		catalog = e2bcatalog.NewRedisSandboxesCatalog(redisClient)
	} else {
		if errors.Is(err, factories.ErrRedisDisabled) {
			l.Warn(ctx, "Redis environment variable is not set, will fallback to in-memory sandboxes catalog that works only with one instance setup")
			catalog = e2bcatalog.NewMemorySandboxesCatalog()
		} else {
			l.Error(ctx, "Failed to create redis client", zap.Error(err))

			return 1
		}
	}

	// TODO: Remove once migrated (ENG-3320)
	redisSecureClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         "",
		RedisClusterURL:  config.RedisSecureClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
	})
	if err == nil {
		defer func() {
			err := factories.CloseCleanly(redisSecureClient)
			if err != nil {
				l.Error(ctx, "Failed to close redis secure client", zap.Error(err))
			}
		}()
		fallbackCatalog := e2bcatalog.NewRedisSandboxesCatalog(redisSecureClient)
		catalog = e2bcatalog.NewRedisFallbackSandboxesCatalog(catalog, fallbackCatalog, featureFlagsClient)
	} else {
		if errors.Is(err, factories.ErrRedisDisabled) {
			l.Warn(ctx, "Redis environment variable is not set, will fallback to in-memory sandboxes catalog that works only with one instance setup")
		} else {
			l.Error(ctx, "Failed to create redis secure client", zap.Error(err))

			return 1
		}
	}

	orchestrators := e2borchestrators.NewOrchestratorsPool(ctx, l, tel.TracerProvider, tel.MeterProvider, orchestratorsSD)

	info := &e2binfo.ServiceInfo{
		NodeID:               nodeID,
		ServiceInstanceID:    uuid.NewString(),
		ServiceVersion:       version,
		ServiceVersionCommit: commitSHA,
		ServiceStartup:       time.Now(),
		Host:                 fmt.Sprintf("%s:%d", env.GetNodeIP(), config.EdgePort),
	}

	// service starts in unhealthy state, and we are waiting for initial health check to pass
	info.SetStatus(ctx, api.Unhealthy)

	// Proxy sandbox http traffic to orchestrator nodes
	trafficProxy, err := e2bproxy.NewClientProxy(
		tel.MeterProvider,
		serviceName,
		config.ProxyPort,
		catalog,
	)
	if err != nil {
		l.Error(ctx, "Failed to create client proxy", zap.Error(err))

		return 1
	}

	authorizationManager := authorization.NewStaticTokenAuthorizationService(config.EdgeSecret)
	edges := e2borchestrators.NewEdgePool(ctx, l, edgeSD, info.Host, authorizationManager)

	var closers []Closeable
	closers = append(closers, orchestrators, edges, featureFlagsClient, catalog)

	edgeApiStore, err := edge.NewEdgeAPIStore(ctx, l, info, edges, orchestrators, catalog, config)
	if err != nil {
		l.Error(ctx, "failed to create edge api store", zap.Error(err))

		return 1
	}

	edgeApiSwagger, err := api.GetSwagger()
	if err != nil {
		l.Error(ctx, "Failed to get swagger", zap.Error(err))

		return 1
	}

	lisAddr := fmt.Sprintf("0.0.0.0:%d", config.EdgePort)
	var lisCfg net.ListenConfig
	lis, err := lisCfg.Listen(ctx, "tcp", lisAddr)
	if err != nil {
		l.Error(ctx, "Failed to listen on edge port", zap.Uint16("port", config.EdgePort), zap.Error(err))

		return 1
	}

	muxServer := cmux.New(lis)

	// Edge Pass Through Proxy for direct communication with orchestrator nodes
	grpcListener := muxServer.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc")) // handler requests for gRPC pass through
	grpcSrv := edgepassthrough.NewNodePassThroughServer(orchestrators, info, authorizationManager, catalog)

	// Edge REST API
	restHttpHandler := edge.NewGinServer(l, edgeApiStore, edgeApiSwagger, authorizationManager)
	restListener := muxServer.Match(cmux.Any())
	restSrv := &http.Server{Handler: restHttpHandler} // handler requests for REST API

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := grpcSrv.Serve(grpcListener)
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.L().Info(ctx, "Edge grpc service shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			logger.L().Error(ctx, "Edge grpc service encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.L().Error(ctx, "Edge grpc service exited without error")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := restSrv.Serve(restListener)
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.L().Info(ctx, "Edge api service shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			logger.L().Error(ctx, "Edge api service encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.L().Error(ctx, "Edge api service exited without error")
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

		edgeRunLogger := l.With(zap.Uint16("edge_port", config.EdgePort))
		edgeRunLogger.Info(ctx, "Edge api starting")

		err := muxServer.Serve()
		if err != nil {
			switch {
			case errors.Is(err, http.ErrServerClosed):
				edgeRunLogger.Info(ctx, "Edge api shutdown successfully")
			case err != nil:
				exitCode.Add(1)
				edgeRunLogger.Error(ctx, "Edge api encountered error", zap.Error(err))
			default:
				edgeRunLogger.Info(ctx, "Edge api exited without error")
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

		proxyRunLogger := l.With(zap.Uint16("proxy_port", config.ProxyPort))
		proxyRunLogger.Info(ctx, "Http proxy starting")

		err := trafficProxy.ListenAndServe(ctx)
		// Add different handling for the error
		switch {
		case errors.Is(err, http.ErrServerClosed):
			proxyRunLogger.Info(ctx, "Http proxy shutdown successfully")
		case err != nil:
			exitCode.Add(1)
			proxyRunLogger.Error(ctx, "Http proxy encountered error", zap.Error(err))
		default:
			// this probably shouldn't happen...
			proxyRunLogger.Error(ctx, "Http proxy exited without error")
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

		shutdownLogger := l.With(zap.Uint16("proxy_port", config.ProxyPort), zap.Uint16("edge_port", config.EdgePort))
		shutdownLogger.Info(ctx, "Shutting down services")

		edgeApiStore.SetDraining(ctx)

		// we should wait for health check manager to notice that we are not ready for new traffic
		shutdownLogger.Info(ctx, "Waiting for draining state propagation", zap.Float64("wait_in_seconds", shutdownDrainingWait.Seconds()))
		time.Sleep(shutdownDrainingWait)

		proxyShutdownCtx, proxyShutdownCtxCancel := context.WithTimeout(ctx, 24*time.Hour)
		defer proxyShutdownCtxCancel()

		// gracefully shutdown the proxy http server
		err := trafficProxy.Shutdown(proxyShutdownCtx)
		if err != nil {
			exitCode.Add(1)
			shutdownLogger.Error(ctx, "Http proxy shutdown error", zap.Error(err))
		} else {
			shutdownLogger.Info(ctx, "Http proxy shutdown successfully")
		}

		edgeApiStore.SetUnhealthy(ctx)

		// wait for the health check manager to notice that we are not healthy at all
		shutdownLogger.Info(ctx, "Waiting for unhealthy state propagation", zap.Float64("wait_in_seconds", shutdownUnhealthyWait.Seconds()))
		time.Sleep(shutdownUnhealthyWait)

		// wait for graceful shutdown of the gRPC server with  pass through proxy
		// it can take some time, because we are waiting for all in-progress requests to finish (sandbox spawning, pausing...)
		grpcSrv.GracefulStop()

		// wait for graceful shutdown of the rest api server with health check
		restShutdownCtx, restShutdownCtxCancel := context.WithTimeout(ctx, shutdownDrainingWait)
		defer restShutdownCtxCancel()

		err = restSrv.Shutdown(restShutdownCtx)
		if err != nil {
			shutdownLogger.Error(ctx, "Edge rest api shutdown error", zap.Error(err))
		}

		// used by instances management for notify that instance is ready for termination
		edgeApiStore.SetTerminating(ctx)

		// close the mux server
		muxServer.Close()

		closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
		defer cancelCloseCtx()

		// close all resources that needs to be closed gracefully
		for _, c := range closers {
			logger.L().Info(ctx, fmt.Sprintf("Closing %T", c))
			if err := c.Close(closeCtx); err != nil { //nolint:contextcheck // TODO: fix this later
				logger.L().Error(ctx, "error during shutdown", zap.Error(err))
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
