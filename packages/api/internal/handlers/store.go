package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/builds"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type APIStore struct {
	Healthy              bool
	posthog              *analyticscollector.PosthogClient
	Tracer               trace.Tracer
	orchestrator         *orchestrator.Orchestrator
	templateManager      *template_manager.TemplateManager
	buildCache           *builds.BuildCache
	db                   *db.DB
	lokiClient           *loki.DefaultClient
	templateCache        *templatecache.TemplateCache
	authCache            *authcache.TeamAuthCache
	templateSpawnCounter *utils.TemplateSpawnCounter

	internalSandboxLogger *sbxlogger.SandboxLogger
	externalSandboxLogger *sbxlogger.SandboxLogger
}

func (a *APIStore) WithInternalSandboxLogger(logger *sbxlogger.SandboxLogger) *APIStore {
	a.internalSandboxLogger = logger
	return a
}

func (a *APIStore) WithExternalSandboxLogger(logger *sbxlogger.SandboxLogger) *APIStore {
	a.externalSandboxLogger = logger
	return a
}

func (a *APIStore) GetInternalSandboxLogger() *sbxlogger.SandboxLogger {
	return a.internalSandboxLogger
}

func (a *APIStore) GetExternalSandboxLogger() *sbxlogger.SandboxLogger {
	return a.externalSandboxLogger
}

func NewAPIStore(ctx context.Context) *APIStore {
	tracer := otel.Tracer("api")

	zap.L().Info("initializing API store and services")

	dbClient, err := db.NewClient()
	if err != nil {
		zap.L().Fatal("initializing Supabase client", zap.Error(err))
	}

	zap.L().Info("created Supabase client")

	posthogClient, posthogErr := analyticscollector.NewPosthogClient()
	if posthogErr != nil {
		zap.L().Fatal("initializing Posthog client", zap.Error(posthogErr))
	}

	nomadConfig := &nomadapi.Config{
		Address:  env.GetEnv("NOMAD_ADDRESS", "http://localhost:4646"),
		SecretID: os.Getenv("NOMAD_TOKEN"),
	}

	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		zap.L().Fatal("initializing Nomad client", zap.Error(err))
	}

	var redisClient *redis.Client
	if rurl := os.Getenv("REDIS_URL"); rurl != "" {
		opts, err := redis.ParseURL(rurl)
		if err != nil {
			zap.L().Fatal("invalid redis URL", zap.String("url", rurl), zap.Error(err))
		}

		redisClient = redis.NewClient(opts)
	} else {
		zap.L().Warn("REDIS_URL not set, using local caches")
	}

	orch, err := orchestrator.New(ctx, tracer, nomadClient, posthogClient, redisClient, dbClient)
	if err != nil {
		zap.L().Fatal("initializing Orchestrator client", zap.Error(err))
	}

	templateManager, err := template_manager.New()
	if err != nil {
		zap.L().Fatal("initializing Template manager client", zap.Error(err))
	}

	var lokiClient *loki.DefaultClient
	if laddr := os.Getenv("LOKI_ADDRESS"); laddr != "" {
		lokiClient = &loki.DefaultClient{
			Address: laddr,
		}
	} else {
		zap.L().Warn("LOKI_ADDRESS not set, disabling Loki client")
	}

	buildCache := builds.NewBuildCache()

	templateCache := templatecache.NewTemplateCache(dbClient)
	authCache := authcache.NewTeamAuthCache(dbClient)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(time.Minute, dbClient)

	return &APIStore{
		Healthy:              true,
		orchestrator:         orch,
		templateManager:      templateManager,
		db:                   dbClient,
		Tracer:               tracer,
		posthog:              posthogClient,
		buildCache:           buildCache,
		lokiClient:           lokiClient,
		templateCache:        templateCache,
		authCache:            authCache,
		templateSpawnCounter: templateSpawnCounter,
	}
}

func (a *APIStore) Close(ctx context.Context) error {
	a.templateSpawnCounter.Close()

	errs := []error{}
	if err := a.posthog.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing Posthog client: %w", err))
	}

	if err := a.orchestrator.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("closing Orchestrator client: %w", err))

	}
	if err := a.templateManager.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing Template manager client: %w", err))
	}

	if err := a.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing database client: %w", err))
	}

	return errors.Join(errs...)
}

// This function wraps sending of an error in the Error format, and
// handling the failure to marshal that.
func (a *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apiErr := api.Error{
		Code:    int32(code),
		Message: message,
	}

	c.Error(fmt.Errorf(message))
	c.JSON(code, apiErr)
}

func (a *APIStore) GetHealth(c *gin.Context) {
	if a.Healthy == true {
		c.String(http.StatusOK, "Health check successful")

		return
	}

	c.String(http.StatusServiceUnavailable, "Service is unavailable")
}

func (a *APIStore) GetTeamFromAPIKey(ctx context.Context, apiKey string) (authcache.AuthTeamInfo, *api.APIError) {
	team, tier, err := a.authCache.Get(ctx, apiKey)
	if err != nil {
		return authcache.AuthTeamInfo{}, &api.APIError{
			Err:       fmt.Errorf("failed to get the team from db for an api key: %w", err),
			ClientMsg: "Cannot get the team for the given API key",
			Code:      http.StatusUnauthorized,
		}
	}

	return authcache.AuthTeamInfo{
		Team: team,
		Tier: tier,
	}, nil
}

func (a *APIStore) GetUserFromAccessToken(ctx context.Context, accessToken string) (uuid.UUID, *api.APIError) {
	userID, err := a.db.GetUserID(ctx, accessToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed to get the user from db for an access token: %w", err),
			ClientMsg: "Cannot get the user for the given access token",
			Code:      http.StatusUnauthorized,
		}
	}

	return *userID, nil
}
