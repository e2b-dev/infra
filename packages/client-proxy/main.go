package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	client_proxy "github.com/e2b-dev/infra/packages/proxy/internal/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	e2bLogger "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ServiceName     = "client-proxy"
	healthCheckPort = 3001
	port            = 3002
)

var commitSHA string

func run() int {
	exitCode := atomic.Int32{}
	wg := sync.WaitGroup{}

	ctx := context.Background()
	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	instanceID := uuid.New().String()
	stopOtlp := telemetry.InitOTLPExporter(ctx, ServiceName, commitSHA, instanceID)
	defer func() {
		err := stopOtlp(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	logger := zap.Must(e2bLogger.NewLogger(ctx, e2bLogger.LoggerConfig{
		ServiceName: ServiceName,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
		Cores:       []zapcore.Core{e2bLogger.GetOTELCore(ServiceName)},
	}))
	defer func() {
		err := logger.Sync()
		if err != nil {
			log.Printf("logger sync error: %v\n", err)
		}
	}()
	zap.ReplaceGlobals(logger)

	logger.Info("Starting client proxy", zap.String("commit", commitSHA), zap.String("instance_id", instanceID))

	healthServer := &http.Server{Addr: fmt.Sprintf(":%d", healthCheckPort)}
	healthServer.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	wg.Add(1)
	go func() {
		// Health check
		defer wg.Done()

		logger.Info("starting health check server", zap.Int("port", healthCheckPort))
		err := healthServer.ListenAndServe()
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.Info("http service shutdown successfully", zap.Int("port", healthCheckPort))
		case err != nil:
			exitCode.Add(1)
			logger.Error("http service encountered error", zap.Int("port", healthCheckPort), zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.Error("http service exited without error", zap.Int("port", healthCheckPort))
		}
	}()

	// Proxy request to the correct node
	proxy := client_proxy.NewClientProxy(port)

	wg.Add(1)
	go func() {
		defer wg.Done()
		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer sigCancel()

		logger.Info("http service starting", zap.Int("port", port))
		err := proxy.ListenAndServe()
		// Add different handling for the error
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.Info("http service shutdown successfully", zap.Int("port", port))
		case err != nil:
			exitCode.Add(1)
			logger.Error("http service encountered error", zap.Int("port", port), zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.Error("http service exited without error", zap.Int("port", port))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-signalCtx.Done()
		logger.Info("shutting down http service", zap.Int("port", healthCheckPort))
		// TODO: We might want to wait for the idle connections to transition to closed instead of closing them.
		if err := healthServer.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", healthCheckPort), zap.Error(err))
		}

		logger.Info("waiting 15 seconds before shutting down http service")
		time.Sleep(15 * time.Second)

		logger.Info("shutting down http service", zap.Int("port", port))
		if err := proxy.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", port), zap.Error(err))
		}
	}()

	wg.Wait()

	return int(exitCode.Load())
}

func main() {
	// Exit, with appropriate code.
	os.Exit(run())
}
