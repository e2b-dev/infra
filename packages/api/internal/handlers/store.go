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
	consulapi "github.com/hashicorp/consul/api"
	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/builds"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

type APIStore struct {
	Ctx             context.Context
	analytics       *analyticscollector.Analytics
	posthog         *PosthogClient
	tracer          trace.Tracer
	instanceCache   *instance.InstanceCache
	orchestrator    *orchestrator.Orchestrator
	templateManager *template_manager.TemplateManager
	buildCache      *builds.BuildCache
	db              *db.DB
	lokiClient      *loki.DefaultClient
	logger          *zap.SugaredLogger
	consulClient    *consulapi.Client
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

	posthogClient, posthogErr := NewPosthogClient(logger)

	if posthogErr != nil {
		logger.Errorf("Error initializing Posthog client\n: %v", posthogErr)
		panic(posthogErr)
	}

	orch, err := orchestrator.New()
	if err != nil {
		logger.Errorf("Error initializing Orchestrator client\n: %v", err)
		panic(err)
	}

	templateManager, err := template_manager.New()
	if err != nil {
		logger.Errorf("Error initializing Template manager client\n: %v", err)
		panic(err)
	}

	var initialInstances []*instance.InstanceInfo

	config := &consulapi.Config{Token: os.Getenv("CONSUL_TOKEN")}
	consulClient, err := consulapi.NewClient(config)
	if err != nil {
		logger.Errorf("Error initializing Consul client\n: %v", err)
		panic(err)
	}

	if env.IsLocal() {
		logger.Info("Skipping loading sandboxes, running locally")
	} else {
		nodes, _, err := consulClient.Catalog().Nodes(nil)
		if err != nil {
			logger.Errorf("Error getting nodes from Consul\n: %v", err)
			panic(err)
		}

		for _, node := range nodes {
			instances, instancesErr := orch.GetInstances(ctx, node.ID[:8])
			if instancesErr != nil {
				logger.Errorf("Error loading current sandboxes\n: %w", instancesErr)
			}

			initialInstances = append(initialInstances, instances...)
		}
	}

	// TODO: rename later
	meter := otel.GetMeterProvider().Meter("nomad")

	instancesCounter, err := meter.Int64UpDownCounter(
		"api.env.instance.running",
		metric.WithDescription(
			"Number of running instances.",
		),
		metric.WithUnit("{instance}"),
	)
	if err != nil {
		panic(err)
	}

	analytics, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Errorf("Error initializing Analytics client\n: %v", err)
	}

	logger.Info("Initialized Analytics client")

	instanceCache := instance.NewCache(analytics.Client, logger, getDeleteInstanceFunction(ctx, orch, analytics, posthogClient, logger), initialInstances, instancesCounter)

	logger.Info("Initialized instance cache")

	if env.IsLocal() {
		logger.Info("Skipping syncing sandboxes, running locally")
	} else {
		go orch.KeepInSync(ctx, instanceCache)
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
		instanceCache:   instanceCache,
		tracer:          tracer,
		analytics:       analytics,
		posthog:         posthogClient,
		buildCache:      buildCache,
		logger:          logger,
		lokiClient:      lokiClient,
		consulClient:    consulClient,
	}
}

func (a *APIStore) Close() {
	a.db.Close()

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

func (a *APIStore) DeleteInstance(instanceID string, purge bool) *api.APIError {
	info, err := a.instanceCache.GetInstance(instanceID)
	if err != nil {
		return &api.APIError{
			Err:       err,
			ClientMsg: "Cannot delete the instance right now",
			Code:      http.StatusInternalServerError,
		}
	}

	return deleteInstance(a.Ctx, a.orchestrator, a.analytics, a.posthog, a.logger, info)
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

func getDeleteInstanceFunction(ctx context.Context, orchestrator *orchestrator.Orchestrator, analytics *analyticscollector.Analytics, posthogClient *PosthogClient, logger *zap.SugaredLogger) func(info instance.InstanceInfo, purge bool) *api.APIError {
	return func(info instance.InstanceInfo, purge bool) *api.APIError {
		return deleteInstance(ctx, orchestrator, analytics, posthogClient, logger, info)
	}
}

func deleteInstance(
	ctx context.Context,
	orchestrator *orchestrator.Orchestrator,
	analytics *analyticscollector.Analytics,
	posthogClient *PosthogClient,
	logger *zap.SugaredLogger,
	info instance.InstanceInfo,
) *api.APIError {
	duration := time.Since(*info.StartTime).Seconds()

	delErr := orchestrator.DeleteInstance(ctx, info.Instance.ClientID, info.Instance.SandboxID)
	if delErr != nil {
		errMsg := fmt.Errorf("cannot delete instance '%s': %w", info.Instance.SandboxID, delErr)

		return &api.APIError{
			Err:       errMsg,
			ClientMsg: "Cannot delete the instance right now",
			Code:      http.StatusInternalServerError,
		}
	}

	if info.TeamID != nil && info.StartTime != nil {
		_, err := analytics.Client.InstanceStopped(ctx, &analyticscollector.InstanceStoppedEvent{
			TeamId:        info.TeamID.String(),
			EnvironmentId: info.Instance.TemplateID,
			InstanceId:    info.Instance.SandboxID,
			Timestamp:     timestamppb.Now(),
			Duration:      float32(duration),
		})
		if err != nil {
			logger.Errorf("error sending Analytics event: %v", err)
		}

		posthogClient.CreateAnalyticsTeamEvent(
			info.TeamID.String(),
			"closed_instance", posthog.NewProperties().
				Set("instance_id", info.Instance.SandboxID).
				Set("environment", info.Instance.TemplateID).
				Set("duration", duration),
		)
	}

	logger.Infof("Closed sandbox '%s' after %f seconds", info.Instance.SandboxID, duration)

	return nil
}
