package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultPort      = 5008
	defaultProxyPort = 5007
	ServiceName      = "orchestrator"
)

var commitSHA string

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	var port uint
	flag.UintVar(&port, "port", defaultPort, "orchestrator server port")

	var proxyPort uint
	flag.UintVar(&proxyPort, "proxy-port", defaultProxyPort, "orchestrator proxy port")
	flag.Parse()

	wg := &sync.WaitGroup{}
	exitCode := &atomic.Int32{}
	telemetrySignal := make(chan struct{})

	// defer waiting on the waitgroup so that this runs even when
	// there's a panic.
	defer wg.Wait()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, server.ServiceName, "no")
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-telemetrySignal
			if err := shutdown(ctx); err != nil {
				log.Printf("telemetry shutdown: %v", err)
				exitCode.Add(1)
			}
		}()
	}

	logger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName: ServiceName,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
		Cores:       []zapcore.Core{logger.GetOTELCore(ServiceName)},
	}))
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      ServiceName,
			IsInternal:       false,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer sbxLoggerExternal.Sync()
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      ServiceName,
			IsInternal:       true,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer sbxLoggerInternal.Sync()
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	log.Println("Starting orchestrator", "commit", commitSHA)

	clientID := consul.GetClientID()
	sessionProxy := proxy.New(proxyPort)

	srv, err := server.New(ctx, port, clientID, sessionProxy)
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error

		defer func() {
			// recover the panic because the service manages a number of go routines
			// that can panic, so catching this here allows for the rest of the process
			// to terminate in a more orderly manner.
			if perr := recover(); perr != nil {
				// many of the panics use log.Panicf which means we're going to log
				// some panic messages twice, but this seems ok, and temporary while
				// we clean up logging.
				log.Printf("caught panic in service: %v", perr)
				exitCode.Add(1)
				err = errors.Join(err, fmt.Errorf("server panic: %v", perr))
			}

			// if we encountered an err, but the signal context was NOT canceled, then
			// the outer context needs to be canceled so the remainder of the service
			// can shutdown.
			if err != nil && sig.Err() == nil {
				log.Printf("service ended early without signal")
				cancel()
			}
		}()

		// this sets the error declared above so the function
		// in the defer can check it.
		if err = srv.Start(ctx); err != nil {
			log.Printf("orchestrator service: %v", err)
			exitCode.Add(1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(telemetrySignal)
		<-sig.Done()
		if err := srv.Close(ctx); err != nil {
			log.Printf("grpc service: %v", err)
			exitCode.Add(1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		errChan := make(chan error, 1)
		go func() {
			errChan <- sessionProxy.Start()
		}()

		select {
		case <-ctx.Done():
		case err = <-errChan:
			if err != nil {
				zap.L().Error("session proxy failed", zap.Error(err))
				exitCode.Add(1)
				cancel()
			}
		}

		// close sandbox proxy, this will wait until all sessions are closed
		defer sessionProxy.Shutdown(context.Background())
	}()

	wg.Wait()

	os.Exit(int(exitCode.Load()))
}
