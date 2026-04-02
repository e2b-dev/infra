package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TeamItem interface {
	TeamID() string
}

// AuthStore abstracts the DB operations needed for auth validation.
type AuthStore[T TeamItem] interface {
	GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (T, error)
	GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (T, error)
	GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error)
	GetTeamAPIKeyHashes(ctx context.Context, teamID uuid.UUID) ([]string, error)
}

// AuthService encapsulates the cache, store, and JWT secrets for auth validation.
type AuthService[T TeamItem] struct {
	store      AuthStore[T]
	teamCache  *AuthCache[T]
	jwtSecrets []string
}

// NewAuthService creates an AuthService with the given store, cache, and JWT secrets.
func NewAuthService[T TeamItem](store AuthStore[T], teamCache *AuthCache[T], jwtSecrets []string) *AuthService[T] {
	return &AuthService[T]{
		store:      store,
		teamCache:  teamCache,
		jwtSecrets: jwtSecrets,
	}
}

// ValidateAPIKey verifies the API key format and fetches the associated team via cache + store.
func (s *AuthService[T]) ValidateAPIKey(ctx context.Context, ginCtx *gin.Context, apiKey string) (T, *APIError) {
	hashedKey, err := keys.VerifyKey(keys.ApiKeyPrefix, apiKey)
	if err != nil {
		var zero T

		return zero, &APIError{
			Err:       fmt.Errorf("failed to verify api key: %w", err),
			ClientMsg: "Invalid API key format",
			Code:      http.StatusUnauthorized,
		}
	}

	result, err := s.teamCache.GetOrSet(ctx, hashedKey, func(ctx context.Context, key string) (T, error) {
		return s.store.GetTeamByHashedAPIKey(ctx, key)
	})
	if err != nil {
		var zero T

		var forbiddenErr *TeamForbiddenError
		if errors.As(err, &forbiddenErr) {
			return zero, &APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *TeamBlockedError
		if errors.As(err, &blockedErr) {
			return zero, &APIError{
				Err:       err,
				ClientMsg: err.Error(),
				Code:      http.StatusForbidden,
			}
		}

		return zero, &APIError{
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

// ValidateAccessToken verifies the access token format and fetches the associated user ID.
func (s *AuthService[T]) ValidateAccessToken(ctx context.Context, ginCtx *gin.Context, accessToken string) (uuid.UUID, *APIError) {
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

// ValidateSupabaseToken parses a Supabase JWT and extracts the user ID.
func (s *AuthService[T]) ValidateSupabaseToken(ctx context.Context, ginCtx *gin.Context, supabaseToken string) (uuid.UUID, *APIError) {
	userID, err := ParseUserIDFromToken(ctx, s.jwtSecrets, supabaseToken)
	if err != nil {
		return uuid.UUID{}, &APIError{
			Err:       err,
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

// ValidateSupabaseTeam extracts the user ID from the gin context and fetches the team via cache + store.
func (s *AuthService[T]) ValidateSupabaseTeam(ctx context.Context, ginCtx *gin.Context, teamID string) (T, *APIError) {
	userID, ok := GetUserID(ginCtx)
	if !ok {
		var zero T

		return zero, &APIError{
			Err:       fmt.Errorf("user ID has invalid type"),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusInternalServerError,
		}
	}

	cacheKey := supabaseTeamCacheKey(userID, teamID)

	result, err := s.teamCache.GetOrSet(ctx, cacheKey, func(ctx context.Context, _ string) (T, error) {
		return s.store.GetTeamByIDAndUserID(ctx, userID, teamID)
	})
	if err != nil {
		var zero T

		var forbiddenErr *TeamForbiddenError
		if errors.As(err, &forbiddenErr) {
			return zero, &APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Forbidden: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		var blockedErr *TeamBlockedError
		if errors.As(err, &blockedErr) {
			return zero, &APIError{
				Err:       fmt.Errorf("failed getting team: %w", err),
				ClientMsg: fmt.Sprintf("Blocked: %s", err.Error()),
				Code:      http.StatusForbidden,
			}
		}

		return zero, &APIError{
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
func (s *AuthService[T]) InvalidateTeamMemberCache(userID uuid.UUID, teamID string) {
	s.teamCache.Invalidate(supabaseTeamCacheKey(userID, teamID))
}

// InvalidateTeamCache queries the team's API key hashes and removes their cached entries.
func (s *AuthService[T]) InvalidateTeamCache(ctx context.Context, teamID uuid.UUID) error {
	hashes, err := s.store.GetTeamAPIKeyHashes(ctx, teamID)
	if err != nil {
		return fmt.Errorf("failed to get team API key hashes: %w", err)
	}

	for _, hash := range hashes {
		s.teamCache.Invalidate(hash)
	}

	return nil
}

func supabaseTeamCacheKey(userID uuid.UUID, teamID string) string {
	return fmt.Sprintf("%s-%s", userID.String(), strings.ToLower(teamID))
}

// Close stops the underlying cache's background refresh goroutines.
func (s *AuthService[T]) Close(ctx context.Context) error {
	return s.teamCache.Close(ctx)
}
