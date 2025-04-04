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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/chdb"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
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
	Healthy              bool
	posthog              *analyticscollector.PosthogClient
	Tracer               trace.Tracer
	orchestrator         *orchestrator.Orchestrator
	templateManager      *template_manager.TemplateManager
	db                   *db.DB
	sqlcDB               *sqlcdb.Client
	lokiClient           *loki.DefaultClient
	templateCache        *templatecache.TemplateCache
	templateBuildsCache  *templatecache.TemplatesBuildCache
	authCache            *authcache.TeamAuthCache
	templateSpawnCounter *utils.TemplateSpawnCounter
	clickhouseStore      chdb.Store
	// should use something like this: https://github.com/spf13/viper
	// but for now this is good
	readMetricsFromClickHouse string
}

func NewAPIStore(ctx context.Context) *APIStore {
	tracer := otel.Tracer("api")

	zap.L().Info("initializing API store and services")

	dbClient, err := db.NewClient()
	if err != nil {
		zap.L().Fatal("initializing Supabase client", zap.Error(err))
	}

	sqlcDB, err := sqlcdb.NewClient(ctx)
	if err != nil {
		zap.L().Fatal("initializing SQLC client", zap.Error(err))
	}

	zap.L().Info("created Supabase client")

	readMetricsFromClickHouse := os.Getenv("READ_METRICS_FROM_CLICKHOUSE")
	var clickhouseStore chdb.Store = nil

	if readMetricsFromClickHouse == "true" {
		clickhouseStore, err = chdb.NewStore(chdb.ClickHouseConfig{
			ConnectionString: os.Getenv("CLICKHOUSE_CONNECTION_STRING"),
			Username:         os.Getenv("CLICKHOUSE_USERNAME"),
			Password:         os.Getenv("CLICKHOUSE_PASSWORD"),
			Database:         os.Getenv("CLICKHOUSE_DATABASE"),
			Debug:            os.Getenv("CLICKHOUSE_DEBUG") == "true",
		})
		if err != nil {
			zap.L().Fatal("initializing ClickHouse store", zap.Error(err))
		}
	}

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

	var redisClusterClient *redis.ClusterClient
	if redisClusterUrl := os.Getenv("REDIS_CLUSTER_URL"); redisClusterUrl != "" {
		redisClusterClient = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        []string{redisClusterUrl},
			MinIdleConns: 1,
		})

		_, err := redisClusterClient.Ping(ctx).Result()
		if err != nil {
			zap.L().Fatal("could not connect to Redis", zap.Error(err))
		}

		zap.L().Info("connected to Redis cluster", zap.String("url", redisClusterUrl))
	} else {
		zap.L().Warn("REDIS_CLUSTER_URL not set, using local caches")
	}

	orch, err := orchestrator.New(ctx, tracer, nomadClient, posthogClient, redisClient, redisClusterClient, dbClient)
	if err != nil {
		zap.L().Fatal("initializing Orchestrator client", zap.Error(err))
	}

	templateBuildsCache := templatecache.NewTemplateBuildCache(dbClient)
	templateManager, err := template_manager.New(ctx, dbClient, templateBuildsCache)
	if err != nil {
		zap.L().Fatal("initializing Template manager client", zap.Error(err))
	}

	// Start the periodic sync of template builds statuses
	go templateManager.BuildsStatusPeriodicalSync(ctx)

	var lokiClient *loki.DefaultClient
	if laddr := os.Getenv("LOKI_ADDRESS"); laddr != "" {
		lokiClient = &loki.DefaultClient{
			Address: laddr,
		}
	} else {
		zap.L().Warn("LOKI_ADDRESS not set, disabling Loki client")
	}

	authCache := authcache.NewTeamAuthCache()
	templateCache := templatecache.NewTemplateCache(dbClient)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(time.Minute, dbClient)

	a := &APIStore{
		Healthy:                   false,
		orchestrator:              orch,
		templateManager:           templateManager,
		db:                        dbClient,
		sqlcDB:                    sqlcDB,
		Tracer:                    tracer,
		posthog:                   posthogClient,
		lokiClient:                lokiClient,
		templateCache:             templateCache,
		templateBuildsCache:       templateBuildsCache,
		authCache:                 authCache,
		templateSpawnCounter:      templateSpawnCounter,
		clickhouseStore:           clickhouseStore,
		readMetricsFromClickHouse: readMetricsFromClickHouse,
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
	team, tier, err := a.authCache.GetOrSet(ctx, apiKey, func(ctx context.Context, key string) (*models.Team, *models.Tier, error) {
		return a.db.GetTeamAuth(ctx, key)
	})
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
	if errors.Is(err, &db.TeamUsageError{}) {
		return authcache.AuthTeamInfo{}, &api.APIError{
			Err:       fmt.Errorf("failed getting team: %w", err),
			ClientMsg: "Team is blocked",
			Code:      http.StatusUnauthorized,
		}
	}
	if err != nil {
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
