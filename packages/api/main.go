package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

func NewGinServer(apiStore *handlers.APIStore, swagger *openapi3.T, port int) *http.Server {
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
	}

	return s
}

func main() {
	fmt.Println("Initializing...")

	port := flag.Int("port", defaultPort, "Port for test HTTP server")
	flag.Parse()

	debug := flag.String("true", "false", "is debug")

	if *debug != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	swagger, err := api.GetSwagger()
	if err != nil {
		log.Printf("Error loading swagger spec\n: %v\n", err)
		os.Exit(1)
	}

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(serviceName, swagger.Info.Version)
		defer shutdown()
	}

	// Create an instance of our handler which satisfies the generated interface
	apiStore := handlers.NewAPIStore()
	defer apiStore.Close()

	s := NewGinServer(apiStore, swagger, *port)

	fmt.Printf("Starting server on port %d\n", *port)
	// And we serve HTTP until the world ends.
	err = s.ListenAndServe()
	if err != nil {
		log.Printf("server error: %v\n", err)
	}
}
