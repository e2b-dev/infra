package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/builds"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

type APIStore struct {
	Ctx             context.Context
	posthog         *analyticscollector.PosthogClient
	tracer          trace.Tracer
	orchestrator    *orchestrator.Orchestrator
	templateManager *template_manager.TemplateManager
	buildCache      *builds.BuildCache
	db              *db.DB
	lokiClient      *loki.DefaultClient
	logger          *zap.SugaredLogger
}

var lokiAddress = os.Getenv("LOKI_ADDRESS")

func NewAPIStore() *APIStore {
	fmt.Println("Initializing API store")

	ctx := context.Background()

	tracer := otel.Tracer("api")

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		log.Printf("Error initializing logger\n: %v\n", err)
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

	templateManager, err := template_manager.New()
	if err != nil {
		logger.Errorf("Error initializing Template manager client\n: %v", err)
		panic(err)
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

	orch, err := orchestrator.New(ctx, nomadClient, logger, posthogClient)
	if err != nil {
		logger.Errorf("Error initializing Orchestrator client\n: %v", err)
		panic(err)
	}

	// TODO: rename later
	meter := otel.GetMeterProvider().Meter("nomad")

	if env.IsLocal() {
		logger.Info("Skipping syncing sandboxes, running locally")
	} else {
		go orch.KeepInSync(ctx, logger)
	}

	var lokiClient *loki.DefaultClient

	if lokiAddress != "" {
		lokiClient = &loki.DefaultClient{
			Address: lokiAddress,
		}
	} else {
		logger.Warn("LOKI_ADDRESS not set, disabling Loki client")
	}

	buildCounter, err := meter.Int64UpDownCounter(
		"api.env.build.running",
		metric.WithDescription(
			"Number of running builds.",
		),
		metric.WithUnit("{build}"),
	)
	if err != nil {
		panic(err)
	}

	buildCache := builds.NewBuildCache(buildCounter)

	return &APIStore{
		Ctx:             ctx,
		orchestrator:    orch,
		templateManager: templateManager,
		db:              dbClient,
		tracer:          tracer,
		posthog:         posthogClient,
		buildCache:      buildCache,
		logger:          logger,
		lokiClient:      lokiClient,
	}
}

func (a *APIStore) Close() {
	a.db.Close()

	err := a.posthog.Close()
	if err != nil {
		a.logger.Errorf("Error closing Posthog client\n: %v", err)
	}

	err = a.orchestrator.Close()
	if err != nil {
		a.logger.Errorf("Error closing Orchestrator client\n: %v", err)
	}
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

func (a *APIStore) GetTeamFromAPIKey(ctx context.Context, apiKey string) (models.Team, *api.APIError) {
	team, err := a.db.GetTeamAuth(ctx, apiKey)
	if err != nil {
		return models.Team{}, &api.APIError{
			Err:       fmt.Errorf("failed to get the team from db for an api key: %w", err),
			ClientMsg: "Cannot get the team for the given API key",
			Code:      http.StatusUnauthorized,
		}
	}

	return *team, nil
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

func (a *APIStore) CheckTeamAccessEnv(ctx context.Context, aliasOrEnvID string, teamID uuid.UUID, public bool) (env *api.Template, build *models.EnvBuild, err error) {
	template, build, err := a.db.GetEnv(ctx, aliasOrEnvID, teamID, public)
	if err != nil {
		return nil, nil, err
	}
	return &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    build.ID.String(),
		Public:     template.Public,
		Aliases:    template.Aliases,
	}, build, nil
}
