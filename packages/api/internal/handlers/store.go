package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	loki "github.com/grafana/loki/pkg/logcli/client"
	nomadapi "github.com/hashicorp/nomad/api"
	middleware "github.com/oapi-codegen/gin-middleware"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var supabaseJWTSecretsString = strings.TrimSpace(os.Getenv("SUPABASE_JWT_SECRETS"))

// minSupabaseJWTSecretLength is the minimum length of a secret used to verify the Supabase JWT.
// This is a security measure to prevent the use of weak secrets (like empty).
const minSupabaseJWTSecretLength = 16

// supabaseJWTSecrets is a list of secrets used to verify the Supabase JWT.
// More secrets are possible in the case of JWT secret rotation where we need to accept
// tokens signed with the old secret for some time.
var supabaseJWTSecrets = strings.Split(supabaseJWTSecretsString, ",")

type APIStore struct {
	Healthy                  bool
	posthog                  *analyticscollector.PosthogClient
	Tracer                   trace.Tracer
	Telemetry                *telemetry.Client
	orchestrator             *orchestrator.Orchestrator
	templateManager          *template_manager.TemplateManager
	db                       *db.DB
	sqlcDB                   *sqlcdb.Client
	lokiClient               *loki.DefaultClient
	templateCache            *templatecache.TemplateCache
	templateBuildsCache      *templatecache.TemplatesBuildCache
	authCache                *authcache.TeamAuthCache
	templateSpawnCounter     *utils.TemplateSpawnCounter
	clickhouseStore          clickhouse.Clickhouse
	envdAccessTokenGenerator *sandbox.EnvdAccessTokenGenerator
	featureFlags             *featureflags.Client
	clustersPool             *edge.Pool
}

func NewAPIStore(ctx context.Context, tel *telemetry.Client) *APIStore {
	tracer := tel.TracerProvider.Tracer("api")

	zap.L().Info("Initializing API store and services")

	dbClient, err := db.NewClient(40, 20)
	if err != nil {
		zap.L().Fatal("Initializing Supabase client", zap.Error(err))
	}

	sqlcDB, err := sqlcdb.NewClient(ctx, sqlcdb.WithMaxConnections(40), sqlcdb.WithMinIdle(5))
	if err != nil {
		zap.L().Fatal("Initializing SQLC client", zap.Error(err))
	}

	zap.L().Info("Created database client")

	var clickhouseStore clickhouse.Clickhouse

	clickhouseConnectionString := strings.TrimSpace(os.Getenv("CLICKHOUSE_CONNECTION_STRING"))
	if clickhouseConnectionString == "" {
		clickhouseStore = clickhouse.NewNoopClient()
	} else {
		clickhouseStore, err = clickhouse.New(clickhouseConnectionString)
		if err != nil {
			zap.L().Fatal("initializing ClickHouse store", zap.Error(err))
		}
	}

	posthogClient, posthogErr := analyticscollector.NewPosthogClient()
	if posthogErr != nil {
		zap.L().Fatal("Initializing Posthog client", zap.Error(posthogErr))
	}

	nomadConfig := &nomadapi.Config{
		Address:  env.GetEnv("NOMAD_ADDRESS", "http://localhost:4646"),
		SecretID: os.Getenv("NOMAD_TOKEN"),
	}

	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		zap.L().Fatal("Initializing Nomad client", zap.Error(err))
	}

	var redisClient redis.UniversalClient
	if redisClusterUrl := os.Getenv("REDIS_CLUSTER_URL"); redisClusterUrl != "" {
		// For managed Redis Cluster in GCP we should use Cluster Client, because
		// > Redis node endpoints can change and can be recycled as nodes are added and removed over time.
		// https://cloud.google.com/memorystore/docs/cluster/cluster-node-specification#cluster_endpoints
		// https://cloud.google.com/memorystore/docs/cluster/client-library-code-samples#go-redis
		redisClient = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        []string{redisClusterUrl},
			MinIdleConns: 1,
		})
	} else if rurl := os.Getenv("REDIS_URL"); rurl != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:         rurl,
			MinIdleConns: 1,
		})
	} else {
		zap.L().Warn("REDIS_URL not set, using local caches")
	}

	if redisClient != nil {
		_, err := redisClient.Ping(ctx).Result()
		if err != nil {
			zap.L().Fatal("Could not connect to Redis", zap.Error(err))
		}

		zap.L().Info("Connected to Redis cluster")
	}

	clustersPool, err := edge.NewPool(ctx, tel, sqlcDB, tracer)
	if err != nil {
		zap.L().Fatal("initializing edge clusters pool failed", zap.Error(err))
	}

	orch, err := orchestrator.New(ctx, tel, tracer, nomadClient, posthogClient, redisClient, dbClient, clustersPool)
	if err != nil {
		zap.L().Fatal("Initializing Orchestrator client", zap.Error(err))
	}

	var lokiClient *loki.DefaultClient
	if laddr := os.Getenv("LOKI_ADDRESS"); laddr != "" {
		lokiClient = &loki.DefaultClient{
			Address: laddr,
		}
	} else {
		zap.L().Warn("LOKI_ADDRESS not set, disabling Loki client")
	}

	authCache := authcache.NewTeamAuthCache()
	templateCache := templatecache.NewTemplateCache(sqlcDB)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(time.Minute, dbClient)

	accessTokenGenerator, err := sandbox.NewEnvdAccessTokenGenerator()
	if err != nil {
		zap.L().Fatal("Initializing access token generator failed", zap.Error(err))
	}

	templateBuildsCache := templatecache.NewTemplateBuildCache(dbClient)
	templateManager, err := template_manager.New(ctx, tracer, tel.TracerProvider, tel.MeterProvider, dbClient, sqlcDB, clustersPool, lokiClient, templateBuildsCache)
	if err != nil {
		zap.L().Fatal("Initializing Template manager client", zap.Error(err))
	}

	// Start the periodic sync of template builds statuses
	go templateManager.BuildsStatusPeriodicalSync(ctx)

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		zap.L().Fatal("failed to create feature flags client", zap.Error(err))
	}

	a := &APIStore{
		Healthy:                  false,
		orchestrator:             orch,
		templateManager:          templateManager,
		db:                       dbClient,
		sqlcDB:                   sqlcDB,
		Telemetry:                tel,
		Tracer:                   tracer,
		posthog:                  posthogClient,
		lokiClient:               lokiClient,
		templateCache:            templateCache,
		templateBuildsCache:      templateBuildsCache,
		authCache:                authCache,
		templateSpawnCounter:     templateSpawnCounter,
		clickhouseStore:          clickhouseStore,
		envdAccessTokenGenerator: accessTokenGenerator,
		clustersPool:             clustersPool,
		featureFlags:             featureFlags,
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
					zap.L().Info("Nodes are ready, setting API as healthy")
					a.Healthy = true
					return
				}
			}
		}
	}()

	return a
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

	if err := a.sqlcDB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing sqlc database client: %w", err))
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

	c.Error(errors.New(message))
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
	team, tier, err := a.authCache.GetOrSet(ctx, apiKey, func(ctx context.Context, key string) (*models.Team, *models.Tier, error) {
		return a.db.GetTeamAuth(ctx, key)
	})
	if err != nil {
		var usageErr *db.TeamForbiddenError
		if errors.As(err, &usageErr) {
			return authcache.AuthTeamInfo{}, &api.APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *db.TeamBlockedError
		if errors.As(err, &blockedErr) {
			return authcache.AuthTeamInfo{}, &api.APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

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

// supabaseClaims defines the claims we expect from the Supabase JWT.
type supabaseClaims struct {
	jwt.RegisteredClaims
}

func getJWTClaims(secrets []string, token string) (*supabaseClaims, error) {
	errs := make([]error, 0)

	for _, secret := range secrets {
		if len(secret) < minSupabaseJWTSecretLength {
			zap.L().Warn("jwt secret is too short and will be ignored", zap.Int("min_length", minSupabaseJWTSecretLength), zap.String("secret_start", secret[:min(3, len(secret))]))

			continue
		}

		// Parse the token with the custom claims.
		token, err := jwt.ParseWithClaims(token, &supabaseClaims{}, func(token *jwt.Token) (interface{}, error) {
			// Verify that the signing method is HMAC (HS256)
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			// Return the secret key used for signing the token.
			return []byte(secret), nil
		})
		if err != nil {
			// This error is ignored because we will try to parse the token with the next secret.
			errs = append(errs, fmt.Errorf("failed to parse supabase token: %w", err))
			continue
		}

		// Extract and return the custom claims if the token is valid.
		if claims, ok := token.Claims.(*supabaseClaims); ok && token.Valid {
			return claims, nil
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to parse supabase token, no secrets found")
	}

	return nil, errors.Join(errs...)
}

func (a *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, supabaseToken string) (uuid.UUID, *api.APIError) {
	claims, err := getJWTClaims(supabaseJWTSecrets, supabaseToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       err,
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	userId, err := claims.GetSubject()
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed getting jwt subject: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	userIDParsed, err := uuid.Parse(userId)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed parsing user uuid: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return userIDParsed, nil
}

func (a *APIStore) GetTeamFromSupabaseToken(ctx context.Context, teamID string) (authcache.AuthTeamInfo, *api.APIError) {
	userID := a.GetUserID(middleware.GetGinContext(ctx))

	team, tier, err := a.authCache.GetOrSet(ctx, teamID, func(ctx context.Context, key string) (*models.Team, *models.Tier, error) {
		return a.db.GetTeamByIDAndUserIDAuth(ctx, teamID, userID)
	})
	if err != nil {
		var usageErr *db.TeamForbiddenError
		if errors.As(err, &usageErr) {
			return authcache.AuthTeamInfo{}, &api.APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Forbidden: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *db.TeamBlockedError
		if errors.As(err, &blockedErr) {
			return authcache.AuthTeamInfo{}, &api.APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Blocked: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		return authcache.AuthTeamInfo{}, &api.APIError{
			Err:       fmt.Errorf("failed getting team: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return authcache.AuthTeamInfo{
		Team: team,
		Tier: tier,
	}, nil
}
