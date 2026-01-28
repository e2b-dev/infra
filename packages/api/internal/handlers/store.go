package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// minSupabaseJWTSecretLength is the minimum length of a secret used to verify the Supabase JWT.
// This is a security measure to prevent the use of weak secrets (like empty).
const minSupabaseJWTSecretLength = 16

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
	authCache            *authcache.TeamAuthCache
	templateSpawnCounter *utils.TemplateSpawnCounter
	clickhouseStore      clickhouse.Clickhouse
	accessTokenGenerator *sandbox.AccessTokenGenerator
	featureFlags         *featureflags.Client
	clusters             *clusters.Pool
}

func NewAPIStore(ctx context.Context, tel *telemetry.Client, config cfg.Config) *APIStore {
	logger.L().Info(ctx, "Initializing API store and services")

	sqlcDB, err := sqlcdb.NewClient(ctx, config.PostgresConnectionString, pool.WithMaxConnections(40), pool.WithMinIdle(5))
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
	})
	if err != nil {
		if errors.Is(err, factories.ErrRedisDisabled) {
			logger.L().Warn(ctx, "REDIS_URL not set, using local caches")
		} else {
			logger.L().Fatal(ctx, "Initializing Redis client", zap.Error(err))
		}
	}

	queryLogsProvider, err := loki.NewLokiQueryProvider(config.LokiURL, config.LokiUser, config.LokiPassword)
	if err != nil {
		logger.L().Fatal(ctx, "error when getting logs query provider", zap.Error(err))
	}

	clusters, err := clusters.NewPool(ctx, tel, sqlcDB, nomadClient, clickhouseStore, queryLogsProvider)
	if err != nil {
		logger.L().Fatal(ctx, "initializing edge clusters pool failed", zap.Error(err))
	}

	featureFlags, err := featureflags.NewClient()
	if err != nil {
		logger.L().Fatal(ctx, "failed to create feature flags client", zap.Error(err))
	}

	accessTokenGenerator, err := sandbox.NewAccessTokenGenerator(config.SandboxAccessTokenHashSeed)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing access token generator failed", zap.Error(err))
	}

	orch, err := orchestrator.New(ctx, config, tel, nomadClient, posthogClient, redisClient, sqlcDB, clusters, featureFlags, accessTokenGenerator)
	if err != nil {
		logger.L().Fatal(ctx, "Initializing Orchestrator client", zap.Error(err))
	}

	authCache := authcache.NewTeamAuthCache()
	templateCache := templatecache.NewTemplateCache(sqlcDB)
	templateSpawnCounter := utils.NewTemplateSpawnCounter(ctx, time.Minute, sqlcDB)

	templateBuildsCache := templatecache.NewTemplateBuildCache(sqlcDB)
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
		authCache:            authCache,
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

	if a.authCache != nil {
		if err := a.authCache.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("closing auth cache: %w", err))
		}
	}

	a.clusters.Close(ctx)

	if err := a.authDB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing auth database client: %w", err))
	}

	if err := a.sqlcDB.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing sqlc database client: %w", err))
	}

	if a.redisClient != nil {
		if err := a.redisClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing redis client: %w", err))
		}
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
	if a.Healthy.Load() {
		c.String(http.StatusOK, "Health check successful")

		return
	}

	c.String(http.StatusServiceUnavailable, "Service is unavailable")
}

func (a *APIStore) GetTeamFromAPIKey(ctx context.Context, _ *gin.Context, apiKey string) (*types.Team, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get team from api key")
	defer span.End()

	hashedApiKey, err := keys.VerifyKey(keys.ApiKeyPrefix, apiKey)
	if err != nil {
		return nil, &api.APIError{
			Err:       fmt.Errorf("failed to verify api key: %w", err),
			ClientMsg: "Invalid API key format",
			Code:      http.StatusUnauthorized,
		}
	}

	team, err := a.authCache.GetOrSet(ctx, hashedApiKey, func(ctx context.Context, key string) (*types.Team, error) {
		return dbapi.GetTeamAuth(ctx, a.authDB, key)
	})
	if err != nil {
		var usageErr *dbapi.TeamForbiddenError
		if errors.As(err, &usageErr) {
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *dbapi.TeamBlockedError
		if errors.As(err, &blockedErr) {
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		return nil, &api.APIError{
			Err:       fmt.Errorf("failed to get the team from db for an api key: %w", err),
			ClientMsg: "Cannot get the team for the given API key",
			Code:      http.StatusUnauthorized,
		}
	}

	return team, nil
}

func (a *APIStore) GetUserFromAccessToken(ctx context.Context, _ *gin.Context, accessToken string) (uuid.UUID, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get user from access token")
	defer span.End()

	hashedToken, err := keys.VerifyKey(keys.AccessTokenPrefix, accessToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed to verify access token: %w", err),
			ClientMsg: "Invalid access token format",
			Code:      http.StatusUnauthorized,
		}
	}

	userID, err := a.authDB.Read.GetUserIDFromAccessToken(ctx, hashedToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed to get the user from db for an access token: %w", err),
			ClientMsg: "Cannot get the user for the given access token",
			Code:      http.StatusUnauthorized,
		}
	}

	return userID, nil
}

// supabaseClaims defines the claims we expect from the Supabase JWT.
type supabaseClaims struct {
	jwt.RegisteredClaims
}

func getJWTClaims(ctx context.Context, secrets []string, token string) (*supabaseClaims, error) {
	ctx, span := tracer.Start(ctx, "get jwt claims")
	defer span.End()

	errs := make([]error, 0)

	for _, secret := range secrets {
		if len(secret) < minSupabaseJWTSecretLength {
			logger.L().Warn(ctx, "jwt secret is too short and will be ignored", zap.Int("min_length", minSupabaseJWTSecretLength), zap.String("secret_start", secret[:min(3, len(secret))]))

			continue
		}

		// Parse the token with the custom claims.
		token, err := jwt.ParseWithClaims(token, &supabaseClaims{}, func(token *jwt.Token) (any, error) {
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

func (a *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, _ *gin.Context, supabaseToken string) (uuid.UUID, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get user id from supabase token")
	defer span.End()

	claims, err := getJWTClaims(ctx, a.config.SupabaseJWTSecrets, supabaseToken)
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

func (a *APIStore) GetTeamFromSupabaseToken(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get team from supabase token")
	defer span.End()

	userID := a.GetUserID(ginCtx)

	cacheKey := fmt.Sprintf("%s-%s", userID.String(), teamID)
	team, err := a.authCache.GetOrSet(ctx, cacheKey, func(ctx context.Context, _ string) (*types.Team, error) {
		return dbapi.GetTeamByIDAndUserIDAuth(ctx, a.authDB, teamID, userID)
	})
	if err != nil {
		var usageErr *dbapi.TeamForbiddenError
		if errors.As(err, &usageErr) {
			return nil, &api.APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Forbidden: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *dbapi.TeamBlockedError
		if errors.As(err, &blockedErr) {
			return nil, &api.APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Blocked: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		return nil, &api.APIError{
			Err:       fmt.Errorf("failed getting team: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return team, nil
}
