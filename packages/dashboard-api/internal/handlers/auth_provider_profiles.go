package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const defaultAdminProfileSearchLimit int32 = 20

func (s *APIStore) PostAdminAuthProviderProfilesResolve(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminAuthProviderProfilesResolveRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	profiles, err := s.userProfiles.GetProfilesByUserID(ctx, body.UserIds)
	if err != nil {
		logger.L().Error(ctx, "failed to resolve auth provider profiles", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to resolve auth provider profiles")

		return
	}

	c.JSON(http.StatusOK, api.AdminAuthProviderProfilesResponse{
		Profiles: apiProfilesFromMap(body.UserIds, profiles),
	})
}

func (s *APIStore) PostAdminAuthProviderProfilesLookupEmail(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminAuthProviderProfilesLookupEmailRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	profiles, err := s.userProfiles.FindProfilesByEmail(ctx, string(body.Email))
	if err != nil {
		logger.L().Error(ctx, "failed to look up auth provider profiles by email", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to look up auth provider profiles")

		return
	}

	c.JSON(http.StatusOK, api.AdminAuthProviderProfilesResponse{
		Profiles: apiProfilesFromProfiles(profiles),
	})
}

func (s *APIStore) PostAdminAuthProviderProfilesSearch(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminAuthProviderProfilesSearchRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	limit := defaultAdminProfileSearchLimit
	if body.Limit != nil {
		limit = *body.Limit
	}

	profiles, err := s.userProfiles.SearchProfilesByEmail(ctx, body.Query, limit)
	if err != nil {
		logger.L().Error(ctx, "failed to search auth provider profiles", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to search auth provider profiles")

		return
	}

	c.JSON(http.StatusOK, api.AdminAuthProviderProfilesResponse{
		Profiles: apiProfilesFromProfiles(profiles),
	})
}

func apiProfilesFromMap(userIDs []uuid.UUID, profiles map[uuid.UUID]userprofile.Profile) []api.AdminAuthProviderProfile {
	result := make([]api.AdminAuthProviderProfile, 0, len(profiles))
	seen := make(map[uuid.UUID]struct{}, len(userIDs))

	for _, userID := range userIDs {
		if _, ok := seen[userID]; !ok {
			seen[userID] = struct{}{}
			if profile, ok := profiles[userID]; ok {
				result = append(result, apiProfileFromProfile(profile))
			}
		}
	}

	return result
}

func apiProfilesFromProfiles(profiles []userprofile.Profile) []api.AdminAuthProviderProfile {
	result := make([]api.AdminAuthProviderProfile, 0, len(profiles))

	for _, profile := range profiles {
		result = append(result, apiProfileFromProfile(profile))
	}

	return result
}

func apiProfileFromProfile(profile userprofile.Profile) api.AdminAuthProviderProfile {
	var email *string
	if profile.Email != "" {
		email = &profile.Email
	}

	return api.AdminAuthProviderProfile{
		UserId: profile.UserID,
		Email:  email,
	}
}
