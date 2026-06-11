package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) DeleteAdminUsersUserId(c *gin.Context, userId api.UserId) {
	ctx := c.Request.Context()

	// Delete the identity provider record (e.g. Ory identity).
	if err := s.userProfiles.DeleteUser(ctx, userId); err != nil {
		logger.L().Error(ctx, "failed to delete user identity provider record", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete user identity provider record")

		return
	}

	// Delete from public.users (cascades to user_identities via FK).
	if err := s.authDB.Write.DeletePublicUser(ctx, userId); err != nil {
		logger.L().Error(ctx, "failed to delete public user", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete public user record")

		return
	}

	c.Status(http.StatusNoContent)
}
