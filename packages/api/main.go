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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/handlers"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	customMiddleware "github.com/e2b-dev/infra/packages/shared/pkg/gin_utils/middleware"
	tracingMiddleware "github.com/e2b-dev/infra/packages/shared/pkg/gin_utils/middleware/otel/tracing"
)

const (
	serviceName          = "orchestration-api"
	maxMultipartMemory   = 1 << 27 // 128 MiB
	maxUploadLimit       = 1 << 28 // 256 MiB
	maxReadHeaderTimeout = 60 * time.Second
	defaultPort          = 80
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
		// customMiddleware.IncludeRoutes(metricsMiddleware.Middleware(serviceName), "/instances"),
		customMiddleware.ExcludeRoutes(gin.LoggerWithWriter(gin.DefaultWriter),
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
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	return s
}

func main() {
	ctx, cancel := context.WithCancel(context.Background()) // root context
	defer cancel()

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer sigCancel()
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
	flag.StringVar(&debug, "true", "false", "is debug")
	flag.Parse()

	log.Println("Starting API service...")
	if debug != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	swagger, err := api.GetSwagger()
	if err != nil {
		log.Fatalf("Error loading swagger spec:\n%v", err)
	}

	var cleanupFns []func() error
	exitCode := &atomic.Int32{}
	cleanup := func() {
		start := time.Now()
		// doing shutdown in parallel to avoid
		// unintentionally: creating shutdown ordering
		// effects.
		wg := &sync.WaitGroup{}
		count := 0
		for idx := range cleanupFns {
			if cleanup := cleanupFns[idx]; cleanup != nil {
				wg.Add(1)
				count++
				go func() {
					defer wg.Done()
					if err := cleanup(); err != nil {
						exitCode.Add(1)
						log.Printf("cleanup operation error: %v", err)
					}
				}()

				// only run each cleanup once (in case cleanup is called
				// explicitly.)
				cleanupFns[idx] = nil
			}
		}
		if count == 0 {
			log.Println("no cleanup operations")
			return
		}
		log.Printf("running %d cleanup operations", count)
		wg.Wait() // this doesn't have a timeout
		log.Printf("%d cleanup operations compleated in %s", count, time.Since(start))
	}
	defer cleanup()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(serviceName, swagger.Info.Version)
		cleanupFns = append(cleanupFns, func() error {
			// shutdown handlers flush buffers upon call and take a context. passing a
			// specific context here so that all timeout configuration is in one place.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			return shutdown(ctx)
		})
	}

	// Create an instance of our handler which satisfies the generated interface
	//  (use the outer context rather than the signal handling
	//   context so it doesn't exit first.)
	apiStore := handlers.NewAPIStore(ctx)
	cleanupFns = append(cleanupFns, apiStore.Close)

	// pass the signal context so that handlers know when shutdown is happening.
	s := NewGinServer(signalCtx, apiStore, swagger, port)

	go func() {
		log.Printf("http service (%d) starting", port)

		// Serve HTTP until shutdown.
		err := s.ListenAndServe()

		switch {
		case errors.Is(err, http.ErrServerClosed):
			log.Printf("http service (%d) shutdown successfully", port)
		case err != nil:
			exitCode.Add(2)
			log.Printf("http service (%d) encountered error: %v", port, err)
		default:
			// this probably shouldn't happen...
			log.Printf("http service (%d) exited without error", port)
		}

		// cancel the parent context, in case the service
		// ended outside of shutdown, (we want everything else
		// to cleanup, and not to get stuck waiting).
		cancel()
	}()

	select {
	case <-signalCtx.Done():
		// shutdown blocks until all active http handlers have returned.
		if err := s.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			log.Printf("http service (%d) shutdown error: %v", port, err)
		}
	case <-ctx.Done():
		log.Println("http service (%d) shutdown outside of signal", port)
	}

	// call cleanup explicitly because defers do not run on os.Exit
	cleanup()

	// Exit, with appropriate code.
	os.Exit(int(exitCode.Load()))
}
