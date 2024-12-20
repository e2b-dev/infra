package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

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
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
)

const (
	defaultRequestLimit = 16
)

var sandboxStartRequestLimit = semaphore.NewWeighted(defaultRequestLimit)

type APIStore struct {
	Ctx                  context.Context
	analytics            *analyticscollector.Analytics
	posthog              *analyticscollector.PosthogClient
	Tracer               trace.Tracer
	orchestrator         *orchestrator.Orchestrator
	templateManager      *template_manager.TemplateManager
	buildCache           *builds.BuildCache
	db                   *db.DB
	lokiClient           *loki.DefaultClient
	logger               *zap.SugaredLogger
	templateCache        *templatecache.TemplateCache
	authCache            *authcache.TeamAuthCache
	templateSpawnCounter *utils.TemplateSpawnCounter
}

var lokiAddress = os.Getenv("LOKI_ADDRESS")

func NewAPIStore() *APIStore {
	fmt.Println("Initializing API store")

	ctx := context.Background()

	tracer := otel.Tracer("api")

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger\n: %v\n", err)
		panic(err)
	}

	dbClient, err := db.NewClient(ctx)
	if err != nil {
		logger.Errorf("Error initializing Supabase client\n: %v", err)
		panic(err)
	}

	logger.Info("Initialized Supabase client")

	posthogClient, posthogErr := analyticscollector.NewPosthogClient(logger)
	if posthogErr != nil {
		logger.Errorf("Error initializing Posthog client\n: %v", posthogErr)
		panic(posthogErr)
	}

	nomadConfig := &nomadapi.Config{
		Address:  env.GetEnv("NOMAD_ADDRESS", "http://localhost:4646"),
		SecretID: os.Getenv("NOMAD_TOKEN"),
	}

	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		logger.Errorf("Error initializing Nomad client\n: %v", err)
		panic(err)
	}

	orch, err := orchestrator.New(ctx, tracer, nomadClient, logger, posthogClient)
	if err != nil {
		logger.Errorf("Error initializing Orchestrator client\n: %v", err)
		panic(err)
	}

	templateManager, err := template_manager.New()
	if err != nil {
		logger.Errorf("Error initializing Template manager client\n: %v", err)
		panic(err)
	}

	var lokiClient *loki.DefaultClient

	if lokiAddress != "" {
		lokiClient = &loki.DefaultClient{
			Address: lokiAddress,
		}
	} else {
		logger.Warn("LOKI_ADDRESS not set, disabling Loki client")
	}

	buildCache := builds.NewBuildCache()

	templateCache := templatecache.NewTemplateCache(dbClient)
	authCache := authcache.NewTeamAuthCache(dbClient)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(time.Minute, dbClient)

	return &APIStore{
		Ctx:                  ctx,
		orchestrator:         orch,
		templateManager:      templateManager,
		db:                   dbClient,
		Tracer:               tracer,
		posthog:              posthogClient,
		buildCache:           buildCache,
		logger:               logger,
		lokiClient:           lokiClient,
		templateCache:        templateCache,
		authCache:            authCache,
		templateSpawnCounter: templateSpawnCounter,
	}
}

func (a *APIStore) Close() {
	a.templateSpawnCounter.Close()

	err := a.analytics.Close()
	if err != nil {
		a.logger.Errorf("Error closing Analytics\n: %v", err)
	}

	err = a.posthog.Close()
	if err != nil {
		a.logger.Errorf("Error closing Posthog client\n: %v", err)
	}

	err = a.orchestrator.Close()
	if err != nil {
		a.logger.Errorf("Error closing Orchestrator client\n: %v", err)
	}
	err = a.templateManager.Close()
	if err != nil {
		a.logger.Errorf("Error closing Template manager client\n: %v", err)
	}

	a.db.Close()
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
	c.String(http.StatusOK, "Health check successful")
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
