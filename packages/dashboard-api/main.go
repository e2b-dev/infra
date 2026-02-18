package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/handlers"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	e2benv "github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	serviceName    = "dashboard-api"
	serviceVersion = "0.1.0"

	defaultPort = 3010

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 75 * time.Second
	idleTimeout       = 620 * time.Second
)

var commitSHA string

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	serviceInstanceID := uuid.New().String()
	nodeID := e2benv.GetNodeID()

	tel, err := telemetry.New(ctx, nodeID, serviceName, commitSHA, serviceVersion, serviceInstanceID)
	if err != nil {
		log.Fatalf("failed to create telemetry: %v", err)
	}
	defer func() {
		if err := tel.Shutdown(ctx); err != nil {
			log.Printf("telemetry shutdown: %v\n", err)
		}
	}()

	l, err := logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   serviceName,
		IsInternal:    true,
		IsDebug:       e2benv.IsDebug(),
		Cores:         []zapcore.Core{logger.GetOTELCore(tel.LogsProvider, serviceName)},
		EnableConsole: true,
	})
	if err != nil {
		log.Fatalf("failed to create logger: %v", err)
	}
	defer l.Sync()
	logger.ReplaceGlobals(ctx, l)

	l.Info(ctx, "Starting dashboard-api service...", zap.String("commit_sha", commitSHA), zap.String("instance_id", serviceInstanceID))

	if !e2benv.IsDebug() {
		gin.SetMode(gin.ReleaseMode)
	}

	db, err := sqlcdb.NewClient(ctx, config.PostgresConnectionString, pool.WithMaxConnections(8))
	if err != nil {
		l.Fatal(ctx, "Initializing database client", zap.Error(err))
	}
	defer db.Close()

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

	apiStore := handlers.NewAPIStore(db, clickhouseClient)

	r := gin.New()
	r.Use(gin.Recovery())

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

	go func() {
		<-signalCtx.Done()
		l.Info(ctx, "Shutting down dashboard-api service...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := s.Shutdown(shutdownCtx); err != nil {
			l.Error(ctx, "HTTP server shutdown error", zap.Error(err))
		}
	}()

	l.Info(ctx, "HTTP service starting", zap.Int("port", config.Port))
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		l.Error(ctx, "HTTP service error", zap.Error(err))
		return 1
	}

	l.Info(ctx, "HTTP service stopped")
	return 0
}

func main() {
	os.Exit(run())
}
