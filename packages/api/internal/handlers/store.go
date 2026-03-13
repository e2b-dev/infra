package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	Healthy              atomic.Bool
	config               cfg.Config
	posthog              *analyticscollector.PosthogClient
	Telemetry            *telemetry.Client
	orchestrator         *orchestrator.Orchestrator
	templateManager      *template_manager.TemplateManager
	sqlcDB               *sqlcdb.Client
	authDB               *authdb.Client
	redisClient          redis.UniversalClient
	templateCache        *templatecache.TemplateCache
	templateBuildsCache  *templatecache.TemplatesBuildCache
	snapshotCache        *snapshotcache.SnapshotCache
	authService          *sharedauth.AuthService[*types.Team]
	templateSpawnCounter *utils.TemplateSpawnCounter
	clickhouseStore      clickhouse.Clickhouse
	accessTokenGenerator *sandbox.AccessTokenGenerator
	featureFlags         *featureflags.Client
	clusters             *clusters.Pool
}

func NewAPIStore(ctx context.Context, tel *telemetry.Client, config cfg.Config, serviceName string) *APIStore {
	logger.L().Info(ctx, "Initializing API store and services")

	sqlcDB, err := sqlcdb.NewClient(ctx, config.PostgresConnectionString, pool.WithMaxConnections(config.DBMaxOpenConnections), pool.WithMinIdle(config.DBMinIdleConnections))
	if err != nil {
		logger.L().Fatal(ctx, "Initializing SQLC client", zap.Error(err))
	}

	authDB, err := authdb.NewClient(
		ctx,
		config.AuthDBConnectionString,
		config.AuthDBReadReplicaConnectionString,
		pool.WithMaxConnections(config.AuthDBMaxOpenConnections),
		pool.WithMinIdle(config.AuthDBMinIdleConnections),
	)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing auth DB client", zap.Error(err))
	}

	logger.L().Info(ctx, "Created database client")

	var clickhouseStore clickhouse.Clickhouse

	clickhouseConnectionString := config.ClickhouseConnectionString
	if clickhouseConnectionString == "" {
		clickhouseStore = clickhouse.NewNoopClient()
	} else {
		clickhouseStore, err = clickhouse.New(clickhouseConnectionString)
		if err != nil {
			logger.L().Fatal(ctx, "initializing ClickHouse store", zap.Error(err))
		}
	}

	posthogClient, posthogErr := analyticscollector.NewPosthogClient(ctx, config.PosthogAPIKey)
	if posthogErr != nil {
		logger.L().Fatal(ctx, "Initializing Posthog client", zap.Error(posthogErr))
	}

	nomadConfig := &nomadapi.Config{
		Address:  config.NomadAddress,
		SecretID: config.NomadToken,
	}

	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing Nomad client", zap.Error(err))
	}
	redisClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
		PoolSize:         config.RedisPoolSize,
	})
	if err != nil {
		logger.L().Fatal(ctx, "Initializing Redis client", zap.Error(err))
	}

	queryLogsProvider, err := loki.NewLokiQueryProvider(config.LokiURL, config.LokiUser, config.LokiPassword)
	if err != nil {
		logger.L().Fatal(ctx, "error when getting logs query provider", zap.Error(err))
	}

	clusters, err := clusters.NewPool(ctx, tel, sqlcDB, nomadClient, clickhouseStore, queryLogsProvider, config)
	if err != nil {
		logger.L().Fatal(ctx, "initializing edge clusters pool failed", zap.Error(err))
	}

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		logger.L().Fatal(ctx, "failed to create feature flags client", zap.Error(err))
	}

	featureFlags.SetServiceName(serviceName)
	featureFlags.SetDeploymentName(config.DomainName)

	accessTokenGenerator, err := sandbox.NewAccessTokenGenerator(config.SandboxAccessTokenHashSeed)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing access token generator failed", zap.Error(err))
	}

	snapshotCache := snapshotcache.NewSnapshotCache(sqlcDB, redisClient)

	orch, err := orchestrator.New(ctx, config, tel, nomadClient, posthogClient, redisClient, sqlcDB, clusters, featureFlags, accessTokenGenerator, snapshotCache)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing Orchestrator client", zap.Error(err))
	}

	authCache := sharedauth.NewAuthCache[*types.Team]()
	authStore := sharedauth.NewAuthStore(authDB)
	authService := sharedauth.NewAuthService[*types.Team](authStore, authCache, config.SupabaseJWTSecrets)
	templateCache := templatecache.NewTemplateCache(sqlcDB, redisClient)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(ctx, time.Minute, sqlcDB)

	templateBuildsCache := templatecache.NewTemplateBuildCache(sqlcDB, redisClient)
	templateManager, err := template_manager.New(sqlcDB, clusters, templateBuildsCache, templateCache, featureFlags)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing Template manager client", zap.Error(err))
	}

	// Start the periodic sync of template builds statuses
	go templateManager.BuildsStatusPeriodicalSync(ctx)

	a := &APIStore{
		config:               config,
		orchestrator:         orch,
		templateManager:      templateManager,
		sqlcDB:               sqlcDB,
		authDB:               authDB,
		Telemetry:            tel,
		posthog:              posthogClient,
		templateCache:        templateCache,
		templateBuildsCache:  templateBuildsCache,
		snapshotCache:        snapshotCache,
		authService:          authService,
		templateSpawnCounter: templateSpawnCounter,
		clickhouseStore:      clickhouseStore,
		accessTokenGenerator: accessTokenGenerator,
		clusters:             clusters,
		featureFlags:         featureFlags,
		redisClient:          redisClient,
	}

	// Wait till there's at least one, otherwise we can't create sandboxes yet
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if orch.NodeCount() != 0 {
					logger.L().Info(ctx, "Nodes are ready, setting API as healthy")
					a.Healthy.Store(true)

					return
				}
			}
		}
	}()

	return a
}

func (a *APIStore) Close(ctx context.Context) error {
	a.templateSpawnCounter.Close(ctx)

	errs := []error{}
	if err := a.posthog.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing Posthog client: %w", err))
	}

	if err := a.orchestrator.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("closing Orchestrator client: %w", err))
	}

	if a.templateCache != nil {
		if err := a.templateCache.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("closing template cache: %w", err))
		}
	}

	if a.authService != nil {
		if err := a.authService.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("closing auth service: %w", err))
		}
	}

	a.clusters.Close(ctx)

	if err := a.authDB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing auth database client: %w", err))
	}

	if err := a.sqlcDB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing sqlc database client: %w", err))
	}

	if a.templateBuildsCache != nil {
		if err := a.templateBuildsCache.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("closing template build cache: %w", err))
		}
	}

	if err := a.snapshotCache.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("closing snapshot cache: %w", err))
	}

	if a.redisClient != nil {
		if err := a.redisClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing redis client: %w", err))
		}
	}

	return errors.Join(errs...)
}

// sendAPIStoreError wraps sending of an error in the Error format.
func (a *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apierrors.SendAPIStoreError(c, code, message)
}

func (a *APIStore) GetHealth(c *gin.Context) {
	if a.Healthy.Load() {
		c.String(http.StatusOK, "Health check successful")

		return
	}

	c.String(http.StatusServiceUnavailable, "Service is unavailable")
}

func (a *APIStore) GetTeamFromAPIKey(ctx context.Context, _ *gin.Context, apiKey string) (*types.Team, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get team from api key")
	defer span.End()

	return a.authService.ValidateAPIKey(ctx, apiKey)
}

func (a *APIStore) GetUserFromAccessToken(ctx context.Context, _ *gin.Context, accessToken string) (uuid.UUID, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get user from access token")
	defer span.End()

	return a.authService.ValidateAccessToken(ctx, accessToken)
}

func (a *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, _ *gin.Context, supabaseToken string) (uuid.UUID, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get user id from supabase token")
	defer span.End()

	return a.authService.ValidateSupabaseToken(ctx, supabaseToken)
}

func (a *APIStore) GetTeamFromSupabaseToken(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get team from supabase token")
	defer span.End()

	return a.authService.ValidateSupabaseTeam(ctx, ginCtx, teamID)
}
