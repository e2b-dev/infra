package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// authStore abstracts the DB operations needed for auth validation.
type authStore interface {
	GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (*types.Team, error)
	GetTeamByID(ctx context.Context, teamID uuid.UUID) (*types.Team, error)
	GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (*types.Team, error)
	GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error)
	GetTeamAPIKeyHashes(ctx context.Context, teamID uuid.UUID) ([]string, error)
}

// Service is the interface implemented by the unexported authService. It
// exposes the auth validation, team lookup, and cache invalidation operations
// used by callers such as APIStore and the dashboard-api handlers.
type Service interface {
	ValidateAPIKey(ctx context.Context, ginCtx *gin.Context, apiKey string) (*types.Team, *APIError)
	ValidateAccessToken(ctx context.Context, ginCtx *gin.Context, accessToken string) (uuid.UUID, *APIError)
	ValidateAuthProviderToken(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)
	ValidateAuthProviderTeam(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *APIError)
	GetTeamByID(ctx context.Context, teamID uuid.UUID) (*types.Team, error)
	InvalidateTeamMemberCache(ctx context.Context, userID uuid.UUID, teamID string)
	InvalidateTeamCache(ctx context.Context, teamID uuid.UUID) error
	Close(ctx context.Context) error
}

// authService encapsulates the cache, store, and JWT verifier for auth validation.
type authService struct {
	store                authStore
	teamCache            *authCache
	authProviderVerifier *Verifier
}

// Compile-time assertion that *authService satisfies the Service interface.
var _ Service = (*authService)(nil)

// NewAuthService wires up the team cache, auth store, identity lookup, and JWT
// verifier from the supplied dependencies. The HTTP client is used for OIDC
// discovery and JWKS fetches.
//
//nolint:revive // returning unexported type is intentional to prevent external instantiation
func NewAuthService(
	ctx context.Context,
	redisClient redis.UniversalClient,
	authDB *authdb.Client,
	providerConfig ProviderConfig,
	httpClient *http.Client,
) (*authService, error) {
	if redisClient == nil {
		return nil, errors.New("redisClient is required")
	}
	if authDB == nil {
		return nil, errors.New("authDB is required")
	}
	if httpClient == nil {
		return nil, errors.New("httpClient is required")
	}

	cache := newAuthCache(redisClient)
	store := newAuthStore(authDB)
	// OIDC bootstrap writes identity rows on the primary immediately before the
	// next authenticated request; using the read replica here races replication lag.
	identityLookup := newAuthIdentityLookup(authDB.Write)
	v, err := NewVerifier(ctx, providerConfig, httpClient, identityLookup)
	if err != nil {
		return nil, fmt.Errorf("initializing auth provider JWT verifier: %w", err)
	}

	return &authService{
		store:                store,
		teamCache:            cache,
		authProviderVerifier: v,
	}, nil
}

// ValidateAPIKey verifies the API key format and fetches the associated team via cache + store.
func (s *authService) ValidateAPIKey(ctx context.Context, ginCtx *gin.Context, apiKey string) (*types.Team, *APIError) {
	hashedKey, err := keys.VerifyKey(keys.ApiKeyPrefix, apiKey)
	if err != nil {
		return nil, &APIError{
			Err:       fmt.Errorf("failed to verify api key: %w", err),
			ClientMsg: "Invalid API key format",
			Code:      http.StatusUnauthorized,
		}
	}

	result, err := s.teamCache.GetOrSet(ctx, hashedKey, func(ctx context.Context, key string) (*types.Team, error) {
		return s.store.GetTeamByHashedAPIKey(ctx, key)
	})
	if err != nil {
		var forbiddenErr *TeamForbiddenError
		if errors.As(err, &forbiddenErr) {
			return nil, &APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		return nil, &APIError{
			Err:       fmt.Errorf("failed to get the team from db for an api key: %w", err),
			ClientMsg: "Cannot get the team for the given API key",
			Code:      http.StatusUnauthorized,
		}
	}

	//nolint:contextcheck // We use the gin request context to set attributes on the parent span.
	telemetry.SetAttributes(ginCtx.Request.Context(),
		telemetry.WithMaskedAPIKey(keys.MaskToken(keys.ApiKeyPrefix, apiKey)),
		telemetry.WithTeamID(result.TeamID()),
	)

	return result, nil
}

// GetTeamByID fetches team auth data via cache + store.
func (s *authService) GetTeamByID(ctx context.Context, teamID uuid.UUID) (*types.Team, error) {
	return s.teamCache.GetOrSet(ctx, teamCacheKey(teamID), func(ctx context.Context, _ string) (*types.Team, error) {
		return s.store.GetTeamByID(ctx, teamID)
	})
}

// ValidateAccessToken verifies the access token format and fetches the associated user ID.
func (s *authService) ValidateAccessToken(ctx context.Context, ginCtx *gin.Context, accessToken string) (uuid.UUID, *APIError) {
	hashedToken, err := keys.VerifyKey(keys.AccessTokenPrefix, accessToken)
	if err != nil {
		return uuid.UUID{}, &APIError{
			Err:       fmt.Errorf("failed to verify access token: %w", err),
			ClientMsg: "Invalid access token format",
			Code:      http.StatusUnauthorized,
		}
	}

	userID, err := s.store.GetUserIDByHashedAccessToken(ctx, hashedToken)
	if err != nil {
		return uuid.UUID{}, &APIError{
			Err:       fmt.Errorf("failed to get the user from db for an access token: %w", err),
			ClientMsg: "Cannot get the user for the given access token",
			Code:      http.StatusUnauthorized,
		}
	}

	//nolint:contextcheck // We use the gin request context to set attributes on the parent span.
	telemetry.SetAttributes(ginCtx.Request.Context(),
		telemetry.WithMaskedAccessToken(keys.MaskToken(keys.AccessTokenPrefix, accessToken)),
		telemetry.WithUserID(userID.String()),
	)

	return userID, nil
}

// ValidateAuthProviderToken verifies a JWT against the configured auth provider and resolves an internal user ID.
//
// When no auth provider verifier is configured (AUTH_PROVIDER_CONFIG is unset),
// every token is denied with 401. This makes "no auth provider" a valid
// configuration: API key / access token flows keep working, but JWT-based
// flows are universally rejected.
func (s *authService) ValidateAuthProviderToken(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError) {
	if s.authProviderVerifier == nil {
		return uuid.UUID{}, &APIError{
			Err:       errors.New("auth provider is not configured"),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return s.validateJWTWithProvider(ctx, ginCtx, s.authProviderVerifier, token, "auth provider")
}

func (s *authService) validateJWTWithProvider(ctx context.Context, ginCtx *gin.Context, v *Verifier, token string, tokenSource string) (uuid.UUID, *APIError) {
	userID, _, err := v.Verify(ctx, token)
	if err != nil {
		return uuid.UUID{}, &APIError{
			Err:       err,
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	if userID == uuid.Nil {
		return uuid.UUID{}, &APIError{
			Err:       fmt.Errorf("%s token user claim is missing or is not an internal UUID", tokenSource),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	//nolint:contextcheck // We use the gin request context to set attributes on the parent span.
	telemetry.SetAttributes(ginCtx.Request.Context(),
		telemetry.WithUserID(userID.String()),
	)

	return userID, nil
}

// ValidateAuthProviderTeam extracts the user ID from the gin context and fetches the team via cache + store.
func (s *authService) ValidateAuthProviderTeam(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *APIError) {
	userID, ok := GetUserID(ginCtx)
	if !ok {
		return nil, &APIError{
			Err:       errors.New("user ID has invalid type"),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusInternalServerError,
		}
	}

	cacheKey := teamMemberCacheKey(userID, teamID)

	result, err := s.teamCache.GetOrSet(ctx, cacheKey, func(ctx context.Context, _ string) (*types.Team, error) {
		return s.store.GetTeamByIDAndUserID(ctx, userID, teamID)
	})
	if err != nil {
		var forbiddenErr *TeamForbiddenError
		if errors.As(err, &forbiddenErr) {
			return nil, &APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Forbidden: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		return nil, &APIError{
			Err:       fmt.Errorf("failed getting team: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	//nolint:contextcheck // We use the gin request context to set attributes on the parent span.
	telemetry.SetAttributes(ginCtx.Request.Context(),
		telemetry.WithUserID(userID.String()),
		telemetry.WithTeamID(result.TeamID()),
	)

	return result, nil
}

// InvalidateTeamMemberCache removes the cached auth entry for a specific user-team pair.
// This should be called when team membership changes (member added or removed).
func (s *authService) InvalidateTeamMemberCache(ctx context.Context, userID uuid.UUID, teamID string) {
	s.teamCache.Invalidate(ctx, teamMemberCacheKey(userID, teamID))
}

// InvalidateTeamCache queries the team's API key hashes and removes their cached entries.
func (s *authService) InvalidateTeamCache(ctx context.Context, teamID uuid.UUID) error {
	s.teamCache.Invalidate(ctx, teamCacheKey(teamID))

	hashes, err := s.store.GetTeamAPIKeyHashes(ctx, teamID)
	if err != nil {
		return fmt.Errorf("failed to get team API key hashes: %w", err)
	}

	for _, hash := range hashes {
		s.teamCache.Invalidate(ctx, hash)
	}

	return nil
}

func teamMemberCacheKey(userID uuid.UUID, teamID string) string {
	return fmt.Sprintf("%s-%s", userID.String(), strings.ToLower(teamID))
}

func teamCacheKey(teamID uuid.UUID) string {
	return fmt.Sprintf("team-%s", teamID.String())
}

// Close stops the underlying cache's background refresh goroutines.
func (s *authService) Close(ctx context.Context) error {
	return s.teamCache.Close(ctx)
}
