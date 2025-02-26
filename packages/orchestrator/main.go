package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
<<<<<<< HEAD
	"net"
||||||| 397e5e6a
	"log"
	"net"
=======
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
>>>>>>> fc4de370eb2036db95799a3dc8d100c0b9f80650

	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.uber.org/zap"
)

const defaultPort = 5008

var logsCollectorAddress = env.GetEnv("LOGS_COLLECTOR_ADDRESS", "")

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	var port uint

	flag.UintVar(&port, "port", defaultPort, "orchestrator server port")
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

<<<<<<< HEAD
	logger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:      server.ServiceName,
		IsInternal:       true,
		IsDevelopment:    env.IsLocal(),
		IsDebug:          env.IsDebug(),
		CollectorAddress: logsCollectorAddress,
	}))
	defer logger.Sync()

	zap.ReplaceGlobals(logger)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		zap.L().Fatal("failed to listen", zap.Error(err))
	}

	s, err := server.New()
||||||| 397e5e6a
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s, err := server.New()
=======
	srv, err := server.New(ctx, port)
>>>>>>> fc4de370eb2036db95799a3dc8d100c0b9f80650
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

<<<<<<< HEAD
	logger.Info("Starting orchestrator server", zap.Int("port", *port))
||||||| 397e5e6a
	log.Printf("starting server on port %d", *port)
=======
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
>>>>>>> fc4de370eb2036db95799a3dc8d100c0b9f80650

<<<<<<< HEAD
	if err := s.Serve(lis); err != nil {
		zap.L().Fatal("failed to serve", zap.Error(err))
	}
||||||| 397e5e6a
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
=======
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

	wg.Wait()

	os.Exit(int(exitCode.Load()))
>>>>>>> fc4de370eb2036db95799a3dc8d100c0b9f80650
}
