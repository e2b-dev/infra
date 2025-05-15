package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/info"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
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

	version = "0.1.0"

	fileLockName = "/orchestrator.lock"
)

var forceStop = env.GetEnv("FORCE_STOP", "false") == "true"
var commitSHA string

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

	success := run(*port, *proxyPort)
	if success == false {
		os.Exit(1)
	}
}

func run(port, proxyPort uint) (success bool) {
	success = true

	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	if !env.IsDevelopment() {
		info, err := os.Stat(fileLockName)
		if err == nil {
			log.Printf("Orchestrator was already started at %s", info.ModTime())
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

	clientID := consul.GetClientID()
	if clientID == "" {
		zap.L().Fatal("client ID is empty")
	}

	services := servicetype.GetServices()
	serviceName := servicetype.GetServiceName(services)

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

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, serviceName, commitSHA, clientID)
		defer func() {
			if err := shutdown(ctx); err != nil {
				log.Printf("telemetry shutdown: %v", err)
				success = false
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
			success = false
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
			success = false
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
			success = false
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

	networkPool, err := network.NewPool(sig, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, clientID)
	if err != nil {
		zap.L().Fatal("failed to create network pool", zap.Error(err))
	}

	devicePool, err := nbd.NewDevicePool(ctx)
	if err != nil {
		zap.L().Fatal("failed to create device pool", zap.Error(err))
	}

	grpcSrv := grpcserver.New(commitSHA)
	tracer := otel.Tracer(serviceName)

	serviceInfo := info.NewInfoContainer(clientID, version, commitSHA)

	_, err = server.New(ctx, grpcSrv, networkPool, devicePool, tracer, serviceInfo, sandboxProxy, sandboxes)
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
			success = false
		}
	}(tmplSbxLoggerExternal)

	var closers []Closeable
	closers = append(closers,
		grpcSrv,
		networkPool,
		devicePool,
		sandboxProxy,
	)

	// Initialize the template manager only if the service is enabled
	if slices.Contains(services, servicetype.TemplateManager) {
		tmpl := tmplserver.New(ctx, tracer, globalLogger, tmplSbxLoggerExternal, grpcSrv, networkPool, devicePool, clientID)

		// Prepend to make sure it's awaited on graceful shutdown
		closers = append([]Closeable{tmpl}, closers...)
	}

	info.NewInfoService(ctx, grpcSrv, serviceInfo, sandboxes)

	g.Go(func() error {
		zap.L().Info("Starting session proxy")
		proxyErr := sandboxProxy.Start()
		if proxyErr != nil {
			serviceError <- proxyErr
		}

		return proxyErr
	})

	g.Go(func() (err error) {
		// this sets the error declared above so the function
		// in the defer can check it.
		grpcErr := grpcSrv.Start(ctx, port)
		if grpcErr != nil {
			grpcErr = fmt.Errorf("grpc server: %w", grpcErr)
			serviceError <- grpcErr
		}

		return grpcErr
	})

	// Wait for the shutdown signal or if some service fails
	select {
	case <-sig.Done():
		zap.L().Info("Shutdown signal received")
	case serviceErr := <-serviceError:
		zap.L().Error("Service error", zap.Error(serviceErr))
		sigCancel()
	}

	closeCtx, cancelCloseCtx := context.WithCancel(context.Background())
	defer cancelCloseCtx()
	if forceStop {
		cancelCloseCtx()
	}

	for _, c := range closers {
		zap.L().Info(fmt.Sprintf("Closing %T, forced: %v", c, forceStop))
		if err := c.Close(closeCtx); err != nil {
			zap.L().Error("error during shutdown", zap.Error(err))
			success = false
		}
	}

	zap.L().Info("Waiting for services to finish")
	if err := g.Wait(); err != nil {
		zap.L().Error("service group error", zap.Error(err))
		success = false
	}

	return success
}
