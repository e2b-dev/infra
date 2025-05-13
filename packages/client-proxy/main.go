package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	e2bLogger "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ServiceName        = "client-proxy"
	TracingServiceName = "edge-api"

	edgePort  = 3001
	proxyPort = 3002

	shutdownDrainingWait  = 30 * time.Second
	shutdownUnhealthyWait = 30 * time.Second
)

var commitSHA string

func run() int {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	instanceID := uuid.New().String()
	stopOTLP := telemetry.InitOTLPExporter(ctx, ServiceName, commitSHA, instanceID)

	defer func() {
		err := stopOTLP(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	logger := zap.Must(
		e2bLogger.NewLogger(
			ctx, e2bLogger.LoggerConfig{
				ServiceName: ServiceName,
				IsInternal:  true,
				IsDebug:     env.IsDebug(),
				Cores:       []zapcore.Core{e2bLogger.GetOTELCore(ServiceName)},
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

	ec2Tags := []string{"e2b"}

	ee, err := service_discovery.NewAwsEc2ServiceDiscovery(ctx, "eu-west-1", ec2Tags, 8080, logger)
	if err != nil {
		logger.Error("failed to create ec2 service discovery", zap.Error(err))
		return 1
	}

	for {
		time.Sleep(5 * time.Second)

		logger.Info("listing nodes")

		result, err := ee.ListNodes(ctx)
		if err != nil {
			logger.Error("failed to list nodes", zap.Error(err))
			continue
		}

		for _, item := range result {
			logger.Info("service discovery item", zap.String("ip", item.NodeIp), zap.Int("port", item.NodePort))
		}
	}

	return 0

	// -----
	// -----
	// -----
	// -----
	// -----
	// -----
	// -----

	/*
		return
		exitCode := atomic.Int32{}
		exitTermination := true

		wg := sync.WaitGroup{}

		//ctx, ctxCancel := context.WithCancel(context.Background())
		//defer ctxCancel()
		//
		signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
		defer sigCancel()

		logger.Info("Starting client proxy", zap.String("commit", commitSHA), zap.String("instance_id", instanceID))

		// Proxy request to the correct node
		proxy, err := e2bProxy.NewClientProxy(proxyPort)
		if err != nil {
			logger.Error("failed to create client proxy", zap.Error(err))
			return 1
		}

		edgeApiDrainingHandler := func(terminate bool) {
			// todo: for now we want to always terminate, but sometimes we dont want to because systemd will restart us
			//exitTermination = terminate
			exitTermination = true

			sigCancel() // trigger shutdown workflow
		}

		tracer := otel.Tracer(TracingServiceName)

		edgeApiStore, err := edge.NewEdgeAPIStore(ctx, logger, tracer, &edgeApiDrainingHandler)
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
		edgerGinServer := edge.NewGinServer(ctx, logger, edgeApiStore, edgePort, edgeApiSwagger)

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

			err := edgerGinServer.ListenAndServe()
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
			if env.IsProduction() {
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
			if env.IsProduction() {
				shutdownLogger.Info("waiting for unhealthy state propagation", zap.Float64("wait_in_seconds", shutdownUnhealthyWait.Seconds()))
				time.Sleep(shutdownUnhealthyWait)
			}

			edgeApiStore.GracefullyShutdown()

			ginErr := edgerGinServer.Shutdown(ctx)
			if ginErr != nil {
				exitCode.Add(1)
				shutdownLogger.Error("edge http service shutdown error", zap.Error(ginErr))
			}

			// todo: when running with systemd, we should not exit because it will be restarted
			// there should be flag for exit disabling or some other way to signal that we are done without restarting process
			// still its should be able to exit because to self-update requests that
			if !exitTermination {
				// todo: implement this
			}
		}()

		wg.Wait()

		return int(exitCode.Load())*/
}

func main() {
	// Exit, with appropriate code.
	os.Exit(run())
}
