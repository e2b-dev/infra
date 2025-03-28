package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	"github.com/e2b-dev/infra/packages/template-manager/internal/server"
	"github.com/e2b-dev/infra/packages/template-manager/internal/test"
	"go.uber.org/zap"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
)

const defaultPort = 5009

var commitSHA string

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testFlag := flag.String("test", "", "run tests")
	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")

	port := flag.Int("port", defaultPort, "Port for test HTTP server")

	log.Println("Starting template manager", "commit", commitSHA)

	flag.Parse()

	if err := constants.CheckRequired(); err != nil {
		log.Fatalf("Validation for environment variables failed: %v", err)
	}

	// If we're running a test, we don't need to start the server
	if *testFlag != "" {
		switch *testFlag {
		case "build":
			test.Build(*templateID, *buildID)
			return 0
		}
	}

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, constants.ServiceName, "no", "no")
		defer shutdown(context.TODO())
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	logger := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceName,
			IsInternal:       true,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer logger.Sync()
	sbxlogger.SetSandboxLoggerExternal(logger)
	zap.ReplaceGlobals(logger)

	// used for logging template build output
	buildLogger := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceName,
			IsInternal:       false,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer buildLogger.Sync()
	sbxlogger.SetSandboxLoggerExternal(buildLogger)

	// Create an instance of our handler which satisfies the generated interface
	s, serverStore := server.New(logger, buildLogger)

	exitCode := &atomic.Int32{}
	exitWg := &sync.WaitGroup{}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// wait until services are shut down properly
	defer exitWg.Wait()

	exitWg.Add(1)
	go func() {
		defer exitWg.Done()

		// in case of server error, we want to cancel the context
		// so we are not waiting for kill signal with a broken server
		defer cancel()

		zap.L().Info(fmt.Sprintf("starting server on port %d", *port))
		if err := s.Serve(lis); err != nil {
			zap.L().Error("failed to serve: %v", zap.Error(err))
			exitCode.Add(1)
		}
	}()

	exitWg.Add(1)
	go func() {
		defer exitWg.Done()

		select {
		case <-ctx.Done():
		case <-sigs:
			zap.L().Info("received signal, shutting down server")

			// shutting down the server, wait for all builds to finish
			err := serverStore.Close(context.TODO())
			if err != nil {
				zap.L().Error("error while shutting down server", zap.Error(err))
				exitCode.Add(1)
			}
		}
	}()

	exitWg.Wait()

	return int(exitCode.Load())
}

func main() {
	// want to call it in separated function so all defer function will be called before
	// hard exiting (os.Exist doesn't call defer functions)
	os.Exit(run())
}
