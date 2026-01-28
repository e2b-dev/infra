package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/handlers"
	customMiddleware "github.com/e2b-dev/infra/packages/api/internal/middleware"
	metricsMiddleware "github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	tracingMiddleware "github.com/e2b-dev/infra/packages/api/internal/middleware/otel/tracing"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	serviceVersion     = "1.0.0"
	serviceName        = "orchestration-api"
	maxMultipartMemory = 1 << 23 // 8 MiB
	maxUploadLimit     = 1 << 24 // 16 MiB

	maxReadHeaderTimeout = 5 * time.Second
	maxReadTimeout       = 10 * time.Second
	maxWriteTimeout      = 75 * time.Second

	// This timeout should be > 600 (GCP LB upstream idle timeout) to prevent race condition
	// https://cloud.google.com/load-balancing/docs/https#timeouts_and_retries%23:~:text=The%20load%20balancer%27s%20backend%20keepalive,is%20greater%20than%20600%20seconds
	idleTimeout = 620 * time.Second

	defaultPort = 80
)

var (
	commitSHA                  string
	expectedMigrationTimestamp string
)

func NewGinServer(ctx context.Context, config cfg.Config, tel *telemetry.Client, l logger.Logger, apiStore *handlers.APIStore, swagger *openapi3.T, port int) *http.Server {
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	r := gin.New()
	// Use the raw (percent-encoded) URL path for route matching so that encoded slashes (%2F)
	// in path params are treated as part of the segment, not as path separators.
	// This is needed for template IDs that contain namespace/alias (e.g. "team-slug/my-template").
	// Param values are still unescaped before reaching handlers (UnescapePathValues defaults to true).
	r.UseRawPath = true

	r.Use(
		// We use custom otel gin middleware because we want to log 4xx errors in the otel
		customMiddleware.ExcludeRoutes(
			tracingMiddleware.Middleware(tel.TracerProvider, serviceName), //nolint:contextcheck // TODO: fix this later
			"/health",
			"/sandboxes/:sandboxID/refreshes",
			"/templates/:templateID/builds/:buildID/logs",
			"/templates/:templateID/builds/:buildID/status",
		),
		customMiddleware.IncludeRoutes(
			metricsMiddleware.Middleware(tel.MeterProvider, serviceName), //nolint:contextcheck // TODO: fix this later
			"/sandboxes",
			"/sandboxes/:sandboxID",
			"/sandboxes/:sandboxID/pause",
			"/sandboxes/:sandboxID/connect",
			"/sandboxes/:sandboxID/resume",
		),
		gin.Recovery(),
	)

	corsConfig := cors.DefaultConfig()
	// Allow all origins
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{
		// Default headers
		"Origin",
		"Content-Length",
		"Content-Type",
		"User-Agent",
		// API Key header
		"Authorization",
		"X-API-Key",
		// Supabase headers
		"X-Supabase-Token",
		"X-Supabase-Team",
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
	r.Use(cors.New(corsConfig))

	// Create a team API Key auth validator
	AuthenticationFunc := auth.CreateAuthenticationFunc(
		config,
		apiStore.GetTeamFromAPIKey,
		apiStore.GetUserFromAccessToken,
		apiStore.GetUserIDFromSupabaseToken,
		apiStore.GetTeamFromSupabaseToken,
	)

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	r.Use(
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(swagger,
			&middleware.Options{
				ErrorHandler: func(c *gin.Context, message string, fallbackStatusCode int) {
					// Override the status code provided by the oapi-codegen/gin-middleware as that is always set to 400 or 404.
					statusCode := max(c.Writer.Status(), fallbackStatusCode)
					utils.ErrorHandler(c, message, statusCode)
				},
				MultiErrorHandler: utils.MultiErrorHandler,
				Options: openapi3filter.Options{
					AuthenticationFunc: AuthenticationFunc,
					// Handle multiple errors as MultiError type
					MultiError: true,
				},
			}),
	)

	r.Use(customMiddleware.InitLaunchDarklyContext)

	r.Use(
		// Request logging must be executed after authorization (if required) is done,
		// so that we can log team ID.
		customMiddleware.ExcludeRoutes(
			func(c *gin.Context) {
				teamID := ""

				// Get team from context, use TeamContextKey
				teamInfo := c.Value(auth.TeamContextKey)
				if teamInfo != nil {
					teamID = teamInfo.(*types.Team).ID.String()
				}

				reqLogger := l
				if teamID != "" {
					reqLogger = l.With(logger.WithTeamID(teamID))
				}

				customMiddleware.LoggingMiddleware(reqLogger, customMiddleware.Config{
					TimeFormat:   time.RFC3339Nano,
					UTC:          true,
					DefaultLevel: zap.InfoLevel,
				})(c)
			},
			"/health",
			"/sandboxes/:sandboxID/refreshes",
			"/templates/:templateID/builds/:buildID/logs",
			"/templates/:templateID/builds/:buildID/status",
		),
	)

	// We now register our store above as the handler for the interface
	api.RegisterHandlersWithOptions(r, apiStore, api.GinServerOptions{
		ErrorHandler: func(c *gin.Context, err error, statusCode int) {
			utils.ErrorHandler(c, err.Error(), statusCode)
		},
	})

	r.MaxMultipartMemory = maxMultipartMemory

	s := &http.Server{
		Handler: r,
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),

		// Configure request timeouts.
		ReadHeaderTimeout: maxReadHeaderTimeout,
		ReadTimeout:       maxReadTimeout,
		WriteTimeout:      maxWriteTimeout,

		// Configure timeouts to be greater than the proxy timeouts.
		IdleTimeout: idleTimeout,

		BaseContext: func(net.Listener) context.Context { return ctx },
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

	serviceInstanceID := uuid.New().String()
	nodeID := env.GetNodeID()

	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create metrics exporter", zap.Error(err))
	}
	defer func() {
		err := tel.Shutdown(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	l := sharedutils.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       env.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
	}))
	defer l.Sync()
	logger.ReplaceGlobals(ctx, l)

	sbxLoggerExternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       false,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer sbxLoggerExternal.Sync()
	sbxlogger.SetSandboxLoggerExternal(sbxLoggerExternal)

	sbxLoggerInternal := sbxlogger.NewLogger(
		ctx,
		tel.LogsProvider,
		sbxlogger.SandboxLoggerConfig{
			ServiceName:      serviceName,
			IsInternal:       true,
			CollectorAddress: env.LogsCollectorAddress(),
		},
	)
	defer sbxLoggerInternal.Sync()
	sbxlogger.SetSandboxLoggerInternal(sbxLoggerInternal)

	// Convert the string expectedMigrationTimestamp  to a int64
	expectedMigration, err := strconv.ParseInt(expectedMigrationTimestamp, 10, 64)
	if err != nil {
		// If expectedMigrationTimestamp is not set, we set it to 0
		l.Warn(ctx, "Failed to parse expected migration timestamp", zap.Error(err))
		expectedMigration = 0
	}

	config, err := cfg.Parse()
	if err != nil {
		logger.L().Fatal(ctx, "Error parsing config", zap.Error(err))
	}

	err = utils.CheckMigrationVersion(ctx, config.PostgresConnectionString, expectedMigration)
	if err != nil {
		l.Fatal(ctx, "failed to check migration version", zap.Error(err))
	}

	l.Info(ctx, "Starting API service...", zap.String("commit_sha", commitSHA), logger.WithServiceInstanceID(serviceInstanceID))
	if debug != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	swagger, err := api.GetSwagger()
	if err != nil {
		// this will call os.Exit: defers won't run, but none
		// need to yet. Change this if this is called later.
		l.Error(ctx, "Error loading swagger spec", zap.Error(err))

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
						l.Error(ctx, "Cleanup operation error", zap.Int("index", idx), zap.Error(err))
					}
				}(cleanup, idx)

				cleanupFns[idx] = nil
			}
		}
		if count == 0 {
			l.Info(ctx, "no cleanup operations")

			return
		}
		l.Info(ctx, "Running cleanup operations", zap.Int("count", count))
		cwg.Wait() // this doesn't have a timeout
		l.Info(ctx, "Cleanup operations completed", zap.Int("count", count), zap.Duration("duration", time.Since(start)))
	}
	cleanupOnce := &sync.Once{}
	cleanup := func() { cleanupOnce.Do(cleanupOp) }
	defer cleanup()

	// Create an instance of our handler which satisfies the generated interface
	//  (use the outer context rather than the signal handling
	//   context so it doesn't exit first.)
	apiStore := handlers.NewAPIStore(ctx, tel, config)
	cleanupFns = append(cleanupFns, apiStore.Close)

	// pass the signal context so that handlers know when shutdown is happening.
	s := NewGinServer(ctx, config, tel, l, apiStore, swagger, port)

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

	wg.Go(func() {
		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer cancel()

		l.Info(ctx, "Http service starting", zap.Int("port", port))

		// Serve HTTP until shutdown.
		err := s.ListenAndServe()

		switch {
		case errors.Is(err, http.ErrServerClosed):
			l.Info(ctx, "Http service shutdown successfully", zap.Int("port", port))
		case err != nil:
			exitCode.Add(1)
			l.Error(ctx, "Http service encountered error", zap.Int("port", port), zap.Error(err))
		default:
			// this probably shouldn't happen...
			l.Info(ctx, "Http service exited without error", zap.Int("port", port))
		}
	})

	wg.Go(func() {
		<-signalCtx.Done()

		// Start returning 503s for health checks
		// to signal that the service is shutting down.
		// This is a bit of a hack, but this way we can properly propagate
		// the health status to the load balancer.
		apiStore.Healthy.Store(false)

		// Skip the delay in local environment for instant shutdown
		if !env.IsLocal() {
			time.Sleep(15 * time.Second)
		}

		// if the parent context `ctx` is canceled the
		// shutdown will return early. This should only happen
		// if there's an error in starting the http service
		// (and would be a noop), or if there's an unhandled
		// panic and defers start running, _probably_ won't
		// even have a chance to return before the program
		// returns.

		if err := s.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			l.Error(ctx, "Http service shutdown error", zap.Int("port", port), zap.Error(err))
		}
	})

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
