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

const maxAdminProfileResolveUserIDs = 100

func (s *APIStore) PostAdminUserProfilesResolve(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminAuthProviderProfilesResolveRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if len(body.UserIds) == 0 || len(body.UserIds) > maxAdminProfileResolveUserIDs {
		s.sendAPIStoreError(c, http.StatusBadRequest, "userIds must contain between 1 and 100 items")

		return
	}

	seen := make(map[uuid.UUID]struct{}, len(body.UserIds))
	for _, userID := range body.UserIds {
		if _, ok := seen[userID]; ok {
			s.sendAPIStoreError(c, http.StatusBadRequest, "userIds must be unique")

			return
		}
		seen[userID] = struct{}{}
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

func (s *APIStore) PostAdminUserProfilesByEmail(c *gin.Context) {
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

func (s *APIStore) GetAdminUserProfilesUserId(c *gin.Context, userId api.UserId) {
	ctx := c.Request.Context()
	profiles, err := s.userProfiles.GetProfilesByUserID(ctx, []uuid.UUID{userId})
	if err != nil {
		logger.L().Error(ctx, "failed to resolve auth provider profile", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to resolve auth provider profile")

		return
	}

	c.JSON(http.StatusOK, api.AdminAuthProviderProfilesResponse{
		Profiles: apiProfilesFromMap([]uuid.UUID{userId}, profiles),
	})
}

func apiProfilesFromMap(userIDs []uuid.UUID, profiles map[uuid.UUID]userprofile.Profile) []api.AdminAuthProviderProfile {
	result := make([]api.AdminAuthProviderProfile, 0, len(profiles))
	seen := make(map[uuid.UUID]struct{}, len(userIDs))

	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}

		profile, ok := profiles[userID]
		if !ok {
			continue
		}

		result = append(result, apiProfileFromProfile(profile))
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
		email = new(profile.Email)
	}

	return api.AdminAuthProviderProfile{
		UserId: profile.UserID,
		Email:  email,
	}
}
