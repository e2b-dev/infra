package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/servicetype"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Closeable interface {
	Close(context.Context) error
}

const (
	defaultPort      = 5008
	defaultProxyPort = 5007
)

var commitSHA string

func main() {
	port := flag.Uint("port", defaultPort, "orchestrator server port")
	proxyPort := flag.Uint("proxy-port", defaultProxyPort, "orchestrator proxy port")
	flag.Parse()
	if *port > math.MaxUint16 {
		log.Fatalf("%d is larger than maximum possible port %d", port, math.MaxInt16)
		os.Exit(1)
	}

	os.Exit(run(*port, *proxyPort))
}

func run(port, proxyPort uint) (result int) {
	result = 0
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	clientID := consul.GetClientID()

	services := servicetype.GetServices()
	serviceName := servicetype.GetServiceName(services)

	var g errgroup.Group
	// defer waiting on the group so that this runs even when
	// there's a panic.
	defer func(g *errgroup.Group) {
		err := g.Wait()
		if err != nil {
			log.Printf("error while shutting down: %v", err)
			result = 1
		}
	}(&g)

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, serviceName, commitSHA, clientID)
		defer func() {
			if err := shutdown(ctx); err != nil {
				log.Printf("telemetry shutdown: %v", err)
				result = 1
			}
		}()
	}

	globalLogger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName: serviceName,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
		Cores:       []zapcore.Core{logger.GetOTELCore(serviceName)},
	}))
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down logger: %v", err)
			result = 1
		}
	}(globalLogger)
	zap.ReplaceGlobals(globalLogger)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down sandbox logger: %v", err)
			result = 1
		}
	}(sbxLoggerExternal)
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       true,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down sandbox logger: %v", err)
			result = 1
		}
	}(sbxLoggerInternal)
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	log.Println("Starting orchestrator", "commit", commitSHA)

	// The sandbox map is shared between the server and the proxy
	// to propagate information about sandbox routing.
	sandboxes := smap.New[*sandbox.Sandbox]()

	sandboxProxy, err := proxy.NewSandboxProxy(proxyPort, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox proxy", zap.Error(err))
	}

	networkPool, err := network.NewPool(ctx, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, clientID)
	if err != nil {
		zap.L().Fatal("failed to create network pool", zap.Error(err))
	}

	grpcSrv := grpcserver.New(commitSHA)
	tracer := otel.Tracer(serviceName)

	_, err = server.New(ctx, grpcSrv, networkPool, tracer, clientID, commitSHA, sandboxProxy, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      constants.ServiceNameTemplate,
			IsInternal:       false,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			log.Printf("error while shutting down template manager sandbox logger: %v", err)
			result = 1
		}
	}(tmplSbxLoggerExternal)
	tmpl := tmplserver.New(ctx, grpcSrv, globalLogger, tmplSbxLoggerExternal, tracer)
	defer tmpl.Close(ctx)

	var closers []Closeable
	closers = append(closers,
		tmpl,
		grpcSrv,
		networkPool,
		sandboxProxy,
	)

	g.Go(func() error {
		zap.L().Info("Starting session proxy")
		return sandboxProxy.Start()
	})

	g.Go(func() (err error) {
		defer func() {
			// recover the panic because the service manages a number of go routines
			// that can panic, so catching this here allows for the rest of the process
			// to terminate in a more orderly manner.
			if perr := recover(); perr != nil {
				// many of the panics use log.Panicf which means we're going to log
				// some panic messages twice, but this seems ok, and temporary while
				// we clean up logging.
				log.Printf("caught panic in service: %v", perr)
				err = errors.Join(err, fmt.Errorf("server panic: %v", perr))
			}
		}()

		// this sets the error declared above so the function
		// in the defer can check it.
		if err = grpcSrv.Start(ctx, port); err != nil {
			return fmt.Errorf("grpc service: %w", err)
		}

		return nil
	})

	<-ctx.Done()
	log.Printf("Shutdown signal received")

	for _, c := range closers {
		if err := c.Close(context.Background()); err != nil {
			log.Printf("error during shutdown: %v", err)
			result = 1
		}
	}

	if err := g.Wait(); err != nil {
		log.Printf("service group error: %v", err)
		result = 1
	}

	return result
}
