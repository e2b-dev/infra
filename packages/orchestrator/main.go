package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Closeable interface {
	Close(context.Context) error
}

const (
	defaultPort           = 5008
	defaultProxyPort      = 5007
	defaultEventProxyPort = 5010

	sandboxMetricExportPeriod = 5 * time.Second

	version = "0.1.0"

	fileLockName = "/orchestrator.lock"
)

var (
	forceStop = env.GetEnv("FORCE_STOP", "false") == "true"
	commitSHA string
)

func main() {
	port := flag.Uint("port", defaultPort, "orchestrator server port")
	proxyPort := flag.Uint("proxy-port", defaultProxyPort, "orchestrator proxy port")
	eventProxyPort := flag.Uint("event-proxy-port", defaultEventProxyPort, "orchestrator event proxy port")
	flag.Parse()

	if *port > math.MaxUint16 {
		log.Fatalf("%d is larger than maximum possible port %d", port, math.MaxInt16)
	}

	if *proxyPort > math.MaxUint16 {
		log.Fatalf("%d is larger than maximum possible proxy port %d", proxyPort, math.MaxInt16)
	}

	success := run(*port, *proxyPort, *eventProxyPort)

	log.Println("Stopping orchestrator, success:", success)

	if success == false {
		os.Exit(1)
	}
}

func run(port, proxyPort, eventProxyPort uint) (success bool) {
	success = true

	services := service.GetServices()

	// Check if the orchestrator crashed and restarted
	// Skip this check in development mode
	// We don't want to lock if the service is running with force stop; the subsequent start would fail.
	if !env.IsDevelopment() && !forceStop {
		info, err := os.Stat(fileLockName)
		if err == nil {
			log.Fatalf("Orchestrator was already started at %s, exiting", info.ModTime())
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

	clientID := service.GetClientID()
	if clientID == "" {
		zap.L().Fatal("client ID is empty")
	}

	serviceName := service.GetServiceName(services)
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

	// Setup telemetry
	var tel *telemetry.Client
	if env.IsLocal() {
		tel = telemetry.NewNoopClient()
	} else {
		var err error
		tel, err = telemetry.New(ctx, serviceName, commitSHA, clientID)
		if err != nil {
			zap.L().Fatal("failed to create metrics exporter", zap.Error(err))
		}
	}
	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("error while shutting down metrics provider: %v", err)
			success = false
		}
	}()

	globalLogger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
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
		tel.LogsProvider,
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
		tel.LogsProvider,
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

	sandboxProxy, err := proxy.NewSandboxProxy(tel.MeterProvider, proxyPort, sandboxes)
	if err != nil {
		zap.L().Fatal("failed to create sandbox proxy", zap.Error(err))
	}

	eventProxy := proxy.NewEventProxy(eventProxyPort)

	tracer := tel.TracerProvider.Tracer(serviceName)

	networkPool, err := network.NewPool(ctx, tel.MeterProvider, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize, clientID, tracer)
	if err != nil {
		zap.L().Fatal("failed to create network pool", zap.Error(err))
	}

	devicePool, err := nbd.NewDevicePool(ctx, tel.MeterProvider)
	if err != nil {
		zap.L().Fatal("failed to create device pool", zap.Error(err))
	}

	serviceInfo := service.NewInfoContainer(clientID, version, commitSHA)

	grpcSrv := grpcserver.New(tel.TracerProvider, tel.MeterProvider, serviceInfo)

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		zap.L().Fatal("failed to create feature flags client", zap.Error(err))
	}

	sandboxObserver, err := telemetry.NewSandboxObserver(ctx, serviceInfo.SourceCommit, serviceInfo.ClientId, sandboxMetricExportPeriod)
	if err != nil {
		zap.L().Fatal("failed to create sandbox observer", zap.Error(err))
	}

	_, err = server.New(ctx, grpcSrv, tel, networkPool, devicePool, tracer, serviceInfo, sandboxProxy, eventProxy, sandboxes, sandboxObserver, featureFlags)
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

	tmplSbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
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
		featureFlags,
		sandboxObserver,
	)

	// Initialize the template manager only if the service is enabled
	if slices.Contains(services, service.TemplateManager) {
		tmpl, err := tmplserver.New(
			ctx,
			tracer,
			tel.MeterProvider,
			globalLogger,
			tmplSbxLoggerExternal,
			grpcSrv,
			networkPool,
			devicePool,
			sandboxProxy,
			sandboxes,
		)
		if err != nil {
			zap.L().Fatal("failed to create template manager", zap.Error(err))
		}

		// Prepend to make sure it's awaited on graceful shutdown
		closers = append([]Closeable{tmpl}, closers...)
	}

	service.NewInfoService(ctx, grpcSrv.GRPCServer(), serviceInfo, sandboxes)

	g.Go(func() error {
		zap.L().Info("Starting session proxy")
		proxyErr := sandboxProxy.Start()
		if proxyErr != nil && !errors.Is(proxyErr, http.ErrServerClosed) {
			proxyErr = fmt.Errorf("proxy server: %w", proxyErr)
			zap.L().Error("error starting proxy server", zap.Error(proxyErr))

			select {
			case serviceError <- proxyErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return proxyErr
		}

		return nil
	})

	g.Go(func() error {
		eventProxyErr := eventProxy.Start()
		if eventProxyErr != nil && !errors.Is(eventProxyErr, http.ErrServerClosed) {
			select {
			case serviceError <- eventProxyErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return eventProxyErr
		}

		return nil
	})

	g.Go(func() (err error) {
		// this sets the error declared above so the function
		// in the defer can check it.
		grpcErr := grpcSrv.Start(ctx, port)
		if grpcErr != nil {
			grpcErr = fmt.Errorf("grpc server: %w", grpcErr)
			zap.L().Error("grpc server error", zap.Error(grpcErr))

			select {
			case serviceError <- grpcErr:
			default:
				// Don't block if the serviceError channel is already closed
				// or if the error is already sent
			}

			return grpcErr
		}

		return nil
	})

	// Wait for the shutdown signal or if some service fails
	select {
	case <-sig.Done():
		zap.L().Info("Shutdown signal received")
	case serviceErr := <-serviceError:
		zap.L().Error("Service error", zap.Error(serviceErr))
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
