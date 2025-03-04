package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	limits "github.com/gin-contrib/size"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/handlers"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	customMiddleware "github.com/e2b-dev/infra/packages/shared/pkg/gin_utils/middleware"
	metricsMiddleware "github.com/e2b-dev/infra/packages/shared/pkg/gin_utils/middleware/otel/metrics"
	tracingMiddleware "github.com/e2b-dev/infra/packages/shared/pkg/gin_utils/middleware/otel/tracing"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	serviceName        = "orchestration-api"
	maxMultipartMemory = 1 << 27 // 128 MiB
	maxUploadLimit     = 1 << 28 // 256 MiB

	maxReadHeaderTimeout = 60 * time.Second
	maxReadTimeout       = 75 * time.Second
	maxWriteTimeout      = 75 * time.Second

	defaultPort = 80
)

var (
	commitSHA string
)

func NewGinServer(ctx context.Context, apiStore *handlers.APIStore, swagger *openapi3.T, port int) *http.Server {
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	r := gin.New()

	r.Use(
		// We use custom otel gin middleware because we want to log 4xx errors in the otel
		customMiddleware.ExcludeRoutes(tracingMiddleware.Middleware(serviceName),
			"/health",
			"/sandboxes/:sandboxID/refreshes",
			"/templates/:templateID/builds/:buildID/logs",
			"/templates/:templateID/builds/:buildID/status",
		),
		customMiddleware.IncludeRoutes(metricsMiddleware.Middleware(serviceName), "/sandboxes"),
		customMiddleware.ExcludeRoutes(ginzap.Ginzap(zap.L(), time.RFC3339Nano, true),
			"/health",
			"/sandboxes/:sandboxID/refreshes",
			"/templates/:templateID/builds/:buildID/logs",
			"/templates/:templateID/builds/:buildID/status",
		),
		gin.Recovery(),
	)

	config := cors.DefaultConfig()
	// Allow all origins
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{
		// Default headers
		"Origin",
		"Content-Length",
		"Content-Type",
		// API Key header
		"Authorization",
		"X-API-Key",
		// Custom headers sent from SDK
		"browser",
		"lang",
		"lang_version",
		"machine",
		"os",
		"package_version",
		"processor",
		"publisher",
		"release",
		"sdk_runtime",
		"system",
	}
	r.Use(cors.New(config))

	// Create a team API Key auth validator
	AuthenticationFunc := auth.CreateAuthenticationFunc(
		apiStore.Tracer,
		apiStore.GetTeamFromAPIKey,
		apiStore.GetUserFromAccessToken,
	)

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	r.Use(
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(swagger,
			&middleware.Options{
				ErrorHandler: utils.ErrorHandler,
				Options: openapi3filter.Options{
					AuthenticationFunc: AuthenticationFunc,
				},
			}),
	)

	// We now register our store above as the handler for the interface
	api.RegisterHandlers(r, apiStore)

	r.MaxMultipartMemory = maxMultipartMemory

	s := &http.Server{
		Handler:           r,
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		ReadHeaderTimeout: maxReadHeaderTimeout,
		ReadTimeout:       maxReadTimeout,
		WriteTimeout:      maxWriteTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	return s
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background()) // root context
	defer cancel()

	// TODO: additional improvements to signal handling/shutdown:
	//   - provide access to root context in the signal handling
	//     context so request scoped work can start background tasks
	//     without needing to make an unattached context.
	//   - provide mechanism to inform shutdown that background
	//     work has completed (waitgroup, counter, etc.) to avoid
	//     exiting early.

	var (
		port  int
		debug string
	)
	flag.IntVar(&port, "port", defaultPort, "Port for test HTTP server")
	flag.StringVar(&debug, "debug", "false", "is debug")
	flag.Parse()

	if !env.IsLocal() {
		otlpCleanup := telemetry.InitOTLPExporter(ctx, serviceName, commitSHA)
		defer otlpCleanup(ctx)
	}

	logger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName: serviceName,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
		Cores:       []zapcore.Core{logger.GetOTELCore(serviceName)},
	}))
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer sbxLoggerExternal.Sync()
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       true,
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		},
	)
	defer sbxLoggerInternal.Sync()
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	logger.Info("Starting API service...", zap.String("commit_sha", commitSHA))
	if debug != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	swagger, err := api.GetSwagger()
	if err != nil {
		// this will call os.Exit: defers won't run, but none
		// need to yet. Change this if this is called later.
		logger.Error("error loading swagger spec", zap.Error(err))
		return 1
	}

	var cleanupFns []func(context.Context) error
	exitCode := &atomic.Int32{}
	cleanupOp := func() {
		// some cleanup functions do work that requires a context. passing shutdown a
		// specific context here so that all timeout configuration is in one place.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		start := time.Now()
		// doing shutdown in parallel to avoid
		// unintentionally: creating shutdown ordering
		// effects.
		cwg := &sync.WaitGroup{}
		count := 0
		for idx := range cleanupFns {
			if cleanup := cleanupFns[idx]; cleanup != nil {
				cwg.Add(1)
				count++
				go func(
					op func(context.Context) error,
					idx int,
				) {
					defer cwg.Done()
					if err := op(ctx); err != nil {
						exitCode.Add(1)
						logger.Error("cleanup operation error", zap.Int("index", idx), zap.Error(err))
					}
				}(cleanup, idx)

				cleanupFns[idx] = nil
			}
		}
		if count == 0 {
			logger.Info("no cleanup operations")
			return
		}
		logger.Info("running cleanup operations", zap.Int("count", count))
		cwg.Wait() // this doesn't have a timeout
		logger.Info("cleanup operations completed", zap.Int("count", count), zap.Duration("duration", time.Since(start)))
	}
	cleanupOnce := &sync.Once{}
	cleanup := func() { cleanupOnce.Do(cleanupOp) }
	defer cleanup()

	// Create an instance of our handler which satisfies the generated interface
	//  (use the outer context rather than the signal handling
	//   context so it doesn't exit first.)
	apiStore := handlers.NewAPIStore(ctx)
	cleanupFns = append(cleanupFns, apiStore.Close)

	// pass the signal context so that handlers know when shutdown is happening.
	s := NewGinServer(ctx, apiStore, swagger, port)

	// ////////////////////////
	//
	// Start the HTTP service

	// set up the signal handlers so that we can trigger a
	// shutdown of the HTTP service when the process catches the
	// specified signal. The parent context isn't canceled until
	// after the HTTP service returns, to avoid terminating
	// connections to databases and other upstream services before
	// the HTTP server has shut down.
	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	wg := &sync.WaitGroup{}

	// in the event of an unhandled panic *still* wait for the
	// HTTP service to terminate:
	defer wg.Wait()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer cancel()

		logger.Info("http service starting", zap.Int("port", port))

		// Serve HTTP until shutdown.
		err := s.ListenAndServe()

		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.Info("http service shutdown successfully", zap.Int("port", port))
		case err != nil:
			exitCode.Add(1)
			logger.Error("http service encountered error", zap.Int("port", port), zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.Info("http service exited without error", zap.Int("port", port))
		}

	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-signalCtx.Done()

		// Start returning 503s for health checks
		// to signal that the service is shutting down.
		// This is a bit of a hack, but this way we can properly propagate
		// the health status to the load balancer.
		apiStore.Healthy = false
		time.Sleep(15 * time.Second)

		// if the parent context `ctx` is canceled the
		// shutdown will return early. This should only happen
		// if there's an error in starting the http service
		// (and would be a noop), or if there's an unhandled
		// panic and defers start running, _probably_ won't
		// even have a chance to return before the program
		// returns.

		if err := s.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", port), zap.Error(err))
		}

	}()

	// wait for the HTTP service to complete shutting down first
	// before doing other cleanup, we're listening for the signal
	// termination in one of these background threads.
	wg.Wait()

	// call cleanup explicitly because defers (from above) do not
	// run on os.Exit.
	cleanup()

	// TODO: wait for additional work to coalesce
	//
	// currently we only wait for the HTTP handlers to return, and
	// then cancel the remaining context and run all of the
	// cleanup functions. Background go routines at this point
	// terminate. Would need to have a goroutine pool or worker
	// coordinator running to manage and track that work.

	// Exit, with appropriate code.
	return int(exitCode.Load())
}

func main() {
	os.Exit(run())
}
