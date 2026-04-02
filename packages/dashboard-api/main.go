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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/handlers"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	e2benv "github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	serviceName    = "dashboard-api"
	serviceVersion = "0.1.0"

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 75 * time.Second
	idleTimeout       = 620 * time.Second
)

var (
	commitSHA                  string
	expectedMigrationTimestamp string
)

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errorCode := atomic.Int32{}

	serviceInstanceID := uuid.New().String()
	nodeID := e2benv.GetNodeID()

	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		logger.L().Fatal(ctx, "failed to create telemetry", zap.Error(err))
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
		logger.L().Fatal(ctx, "failed to create logger", zap.Error(err))
	}
	defer l.Sync()
	logger.ReplaceGlobals(ctx, l)

	config, err := cfg.Parse()
	if err != nil {
		l.Fatal(ctx, "failed to parse config", zap.Error(err))
	}

	l.Info(ctx, "Starting dashboard-api service...", zap.String("commit_sha", commitSHA), zap.String("instance_id", serviceInstanceID))

	expectedMigration, err := strconv.ParseInt(expectedMigrationTimestamp, 10, 64)
	if err != nil {
		l.Warn(ctx, "Failed to parse expected migration timestamp", zap.Error(err))
		expectedMigration = 0
	}

	err = sqlcdb.CheckMigrationVersion(ctx, config.PostgresConnectionString, expectedMigration)
	if err != nil {
		l.Fatal(ctx, "failed to check migration version", zap.Error(err))
	}

	if !e2benv.IsDebug() {
		gin.SetMode(gin.ReleaseMode)
	}

	db, err := sqlcdb.NewClient(
		ctx,
		config.PostgresConnectionString,
		pool.WithMaxConnections(8),
	)
	if err != nil {
		l.Fatal(ctx, "Initializing database client", zap.Error(err))
	}
	defer db.Close()

	authDB, err := authdb.NewClient(
		ctx,
		config.AuthDBConnectionString,
		config.AuthDBReadReplicaConnectionString,
		pool.WithMaxConnections(8),
	)
	if err != nil {
		l.Fatal(ctx, "Initializing auth database client", zap.Error(err))
	}
	defer authDB.Close()

	var clickhouseClient clickhouse.Clickhouse
	if config.ClickhouseConnectionString == "" {
		clickhouseClient = clickhouse.NewNoopClient()
	} else {
		clickhouseClient, err = clickhouse.New(config.ClickhouseConnectionString)
		if err != nil {
			l.Fatal(ctx, "Initializing ClickHouse client", zap.Error(err))
		}
		defer clickhouseClient.Close(ctx)
	}

	redisClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
	})
	if err != nil {
		l.Fatal(ctx, "Initializing Redis client", zap.Error(err))
	}
	defer func() {
		if err := factories.CloseCleanly(redisClient); err != nil {
			l.Error(ctx, "Failed to close Redis client", zap.Error(err))
		}
	}()

	authCache := sharedauth.NewAuthCache[*types.Team](redisClient)
	authStore := sharedauth.NewAuthStore(authDB)
	authService := sharedauth.NewAuthService[*types.Team](authStore, authCache, config.SupabaseJWTSecrets)
	defer authService.Close(ctx)

	apiStore := handlers.NewAPIStore(config, db, authDB, clickhouseClient, authService)

	swagger, err := api.GetSwagger()
	if err != nil {
		l.Fatal(ctx, "Error loading swagger spec", zap.Error(err))
	}
	swagger.Servers = nil

	authenticationFunc := sharedauth.CreateAuthenticationFunc(
		[]sharedauth.Authenticator{
			sharedauth.NewSupabaseTokenAuthenticator(apiStore.GetUserIDFromSupabaseToken),
			sharedauth.NewSupabaseTeamAuthenticator(apiStore.GetTeamFromSupabaseToken),
		},
		nil,
	)

	r := gin.New()
	r.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{
		"Origin",
		"Content-Length",
		"Content-Type",
		sharedauth.HeaderSupabaseToken,
		sharedauth.HeaderSupabaseTeam,
	}
	r.Use(cors.New(corsConfig))

	r.Use(sharedmiddleware.LoggingMiddleware(l, sharedmiddleware.Config{
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
	}))

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

	api.RegisterHandlers(r, apiStore)

	s := &http.Server{
		Handler:           r,
		Addr:              fmt.Sprintf("0.0.0.0:%d", config.Port),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	wg := sync.WaitGroup{}

	wg.Go(func() {
		<-signalCtx.Done()
		l.Info(ctx, "Shutting down dashboard-api service...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer shutdownCancel()

		if err := s.Shutdown(shutdownCtx); err != nil {
			l.Error(ctx, "HTTP server shutdown error", zap.Error(err))

			errorCode.Add(1)
		}
	})

	l.Info(ctx, "HTTP service starting", zap.Int("port", config.Port))
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		l.Error(ctx, "HTTP service error", zap.Error(err))

		errorCode.Add(1)
	} else {
		l.Info(ctx, "HTTP service stopped")
	}

	wg.Wait()

	return int(errorCode.Load())
}

func main() {
	os.Exit(run())
}
