package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/handlers"
	dashboardmiddleware "github.com/e2b-dev/infra/packages/dashboard-api/internal/middleware"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	e2benv "github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	"github.com/e2b-dev/infra/packages/shared/pkg/httpserver"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware"
	metricsmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/metrics"
	tracingmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/tracing"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	serviceName    = "dashboard-api"
	serviceVersion = "0.1.0"

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 75 * time.Second
	requestTimeout    = 70 * time.Second
	idleTimeout       = 620 * time.Second
	shutdownTimeout   = 30 * time.Second
)

var (
	commitSHA                  string
	expectedMigrationTimestamp string
)

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serviceInstanceID := uuid.New().String()
	nodeID := e2benv.GetNodeID()

	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		log.Printf("failed to create telemetry: %v\n", err)

		return 1
	}
	defer func() {
		if err := tel.Shutdown(ctx); err != nil {
			log.Printf("telemetry shutdown: %v\n", err)
		}
	}()

	l, err := logger.NewLogger(logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       e2benv.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
	})
	if err != nil {
		log.Printf("failed to create logger: %v\n", err)

		return 1
	}
	defer l.Sync()
	logger.ReplaceGlobals(ctx, l)

	config, err := cfg.Parse()
	if err != nil {
		fields := []zap.Field{zap.Error(err)}
		if condition, ok := cfg.ParseFailureCondition(err); ok {
			fields = append(fields, zap.String("config_failure_condition", string(condition)))
		}
		l.Error(ctx, "failed to parse config", fields...)

		return 1
	}

	l.Info(ctx, "Starting dashboard-api service...", zap.String("commit_sha", commitSHA), zap.String("instance_id", serviceInstanceID))

	expectedMigration, err := strconv.ParseInt(expectedMigrationTimestamp, 10, 64)
	if err != nil {
		l.Warn(ctx, "Failed to parse expected migration timestamp", zap.Error(err))
		expectedMigration = 0
	}

	err = sqlcdb.CheckMigrationVersion(ctx, config.PostgresConnectionString, expectedMigration)
	if err != nil {
		l.Error(ctx, "failed to check migration version", zap.Error(err))

		return 1
	}

	db, err := sqlcdb.NewClient(
		ctx,
		config.PostgresConnectionString,
		pool.WithMaxConnections(8),
	)
	if err != nil {
		l.Error(ctx, "Initializing database client", zap.Error(err))

		return 1
	}
	defer db.Close()

	authDB, err := authdb.NewClient(
		ctx,
		config.AuthDBConnectionString,
		config.AuthDBReadReplicaConnectionString,
		pool.WithMaxConnections(8),
	)
	if err != nil {
		l.Error(ctx, "Initializing auth database client", zap.Error(err))

		return 1
	}
	defer authDB.Close()

	supabaseDB, err := supabasedb.NewClient(
		ctx,
		config.SupabaseDBConnectionString,
		pool.WithMaxConnections(3),
	)
	if err != nil {
		l.Error(ctx, "Initializing supabase database client", zap.Error(err))

		return 1
	}
	defer supabaseDB.Close()

	var clickhouseClient clickhouse.Clickhouse
	if config.ClickhouseConnectionString == "" {
		clickhouseClient = clickhouse.NewNoopClient()
	} else {
		clickhouseClient, err = clickhouse.New(config.ClickhouseConnectionString)
		if err != nil {
			l.Error(ctx, "Initializing ClickHouse client", zap.Error(err))

			return 1
		}
		defer clickhouseClient.Close(ctx)
	}

	redisClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
	})
	if err != nil {
		l.Error(ctx, "Initializing Redis client", zap.Error(err))

		return 1
	}
	defer func() {
		if err := factories.CloseCleanly(redisClient); err != nil {
			l.Error(ctx, "Failed to close Redis client", zap.Error(err))
		}
	}()

	authClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	authService, err := sharedauth.NewAuthService(ctx, redisClient, authDB, config.AuthProvider, authClient)
	if err != nil {
		l.Error(ctx, "Initializing auth service", zap.Error(err))

		return 1
	}
	defer authService.Close(ctx)

	teamProvisionSink, err := internalteamprovision.NewProvisionSink(
		ctx,
		config.BillingServerURL,
		config.BillingServerAPIToken,
		supabaseDB,
	)
	if err != nil {
		l.Error(ctx, "initializing team provision sink", zap.Error(err))

		return 1
	}

	userProfiles, err := buildUserProfileProvider(config, supabaseDB, authDB, authClient)
	if err != nil {
		l.Error(ctx, "Initializing user profile provider", zap.Error(err))

		return 1
	}

	apiStore := handlers.NewAPIStore(config, db, authDB, supabaseDB, clickhouseClient, authService, teamProvisionSink, userProfiles)

	swagger, err := api.GetSwagger()
	if err != nil {
		l.Error(ctx, "Error loading swagger spec", zap.Error(err))

		return 1
	}
	swagger.Servers = nil

	authenticationFunc := sharedauth.CreateAuthenticationFunc(
		[]sharedauth.Authenticator{
			sharedauth.NewAdminApiKeyAuthenticator(config.AdminToken),
			sharedauth.NewAuthProviderBearerAuthenticator(apiStore.GetUserIDFromAuthProviderToken),
			sharedauth.NewSupabaseTokenAuthenticator(apiStore.GetUserIDFromAuthProviderToken),
			sharedauth.NewSupabaseTeamAuthenticator(apiStore.GetTeamFromSupabaseToken),
			sharedauth.NewAuthProviderTeamAuthenticator(apiStore.GetTeamFromSupabaseToken),
		},
		nil,
	)

	s := newHTTPServer(config.Port, l, tel, swagger, authenticationFunc, apiStore)

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	l.Info(ctx, "HTTP service starting", zap.Int("port", config.Port))
	runErr := waitForServiceStop(signalCtx, startHTTPServer(s))
	if runErr != nil {
		l.Error(ctx, "dashboard-api runtime error", zap.Error(runErr))
	} else {
		l.Info(ctx, "Shutting down dashboard-api service...")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer shutdownCancel()

	if err := shutdownService(shutdownCtx, s); err != nil {
		l.Error(ctx, "dashboard-api shutdown error", zap.Error(err))

		return 1
	}

	if runErr != nil {
		return 1
	}

	l.Info(ctx, "dashboard-api service stopped")

	return 0
}

func main() {
	os.Exit(run())
}

func newHTTPServer(
	port int,
	l logger.Logger,
	tel *telemetry.Client,
	swagger *openapi3.T,
	authenticationFunc openapi3filter.AuthenticationFunc,
	apiStore *handlers.APIStore,
) *http.Server {
	r := gin.New()
	r.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{
		"Origin",
		"Content-Length",
		"Content-Type",
		sharedauth.HeaderAuthorization,
		sharedauth.HeaderAdminToken,
		sharedauth.HeaderSupabaseToken,
		sharedauth.HeaderSupabaseTeam,
		sharedauth.HeaderTeamID,
	}
	r.Use(cors.New(corsConfig))

	r.Use(
		sharedmiddleware.ExcludeRoutes(
			tracingmiddleware.Middleware(tel.TracerProvider, serviceName),
			"/health",
		),
		metricsmiddleware.Middleware(
			tel.MeterProvider,
			serviceName,
			metricsmiddleware.WithShouldRecordFunc(func(_ string, route string, _ *http.Request) bool {
				return route != "/health"
			}),
		),
		sharedmiddleware.LoggingMiddleware(l, sharedmiddleware.Config{
			TimeFormat:   time.RFC3339Nano,
			UTC:          true,
			DefaultLevel: zap.InfoLevel,
			SkipPaths:    []string{"/health"},
			Context: func(c *gin.Context) []zapcore.Field {
				if teamInfo, ok := sharedauth.GetTeamInfo(c); ok {
					return []zapcore.Field{logger.WithTeamID(teamInfo.ID.String())}
				}

				return nil
			},
		}),
		sharedmiddleware.RequestTimeout(requestTimeout),
	)

	r.Use(
		middleware.OapiRequestValidatorWithOptions(swagger,
			&middleware.Options{
				ErrorHandler: func(c *gin.Context, message string, statusCode int) {
					statusCode = max(c.Writer.Status(), statusCode)
					c.AbortWithStatusJSON(statusCode, gin.H{
						"code":    statusCode,
						"message": message,
					})
				},
				MultiErrorHandler: func(me openapi3.MultiError) error {
					msgs := make([]string, 0, len(me))
					for _, e := range me {
						msgs = append(msgs, e.Error())
					}

					return fmt.Errorf("%s", strings.Join(msgs, "; "))
				},
				Options: openapi3filter.Options{
					AuthenticationFunc: authenticationFunc,
				},
			}),
	)

	r.Use(dashboardmiddleware.EnforceBlockedTeam())

	api.RegisterHandlers(r, apiStore)

	s := &http.Server{
		Handler:           r,
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	httpserver.ConfigureH2C(s)

	return s
}

func startHTTPServer(s *http.Server) <-chan error {
	errCh := make(chan error, 1)

	go func() {
		err := s.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			errCh <- nil

			return
		}

		errCh <- err
	}()

	return errCh
}

func waitForServiceStop(signalCtx context.Context, httpErrCh <-chan error) error {
	select {
	case <-signalCtx.Done():
		return nil
	case err := <-httpErrCh:
		if err == nil {
			return errors.New("http service stopped unexpectedly")
		}

		return fmt.Errorf("http service error: %w", err)
	}
}

func shutdownService(ctx context.Context, s *http.Server) error {
	if err := s.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	return nil
}

func buildUserProfileProvider(config cfg.Config, supabaseDB *supabasedb.Client, authDB *authdb.Client, httpClient *http.Client) (userprofile.Provider, error) {
	supaProvider := userprofile.NewSupabaseProvider(supabaseDB)

	var oryProvider userprofile.Provider
	if config.UserProfileProvider.RequiresOry() {
		// identity rows are written on the primary inside the bootstrap tx;
		// reading them from the read replica races replication lag, so resolve
		// (issuer, subject) <-> user_id mappings on the primary.
		provider, err := userprofile.NewOryProvider(userprofile.OryConfig{
			HTTPClient: httpClient,
			SDKURL:     config.OrySDKURL,
			Token:      config.OryProjectAPIToken,
			Issuer:     config.OryIssuerURL,
			Resolver:   authDB.Write,
		})
		if err != nil {
			return nil, fmt.Errorf("build ory user profile provider: %w", err)
		}
		oryProvider = provider
	}

	return userprofile.NewProvider(config.UserProfileProvider, supaProvider, oryProvider)
}
