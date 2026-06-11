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

	// Resolve the external identity references while user_identities still exists.
	handle, err := s.userProfiles.PrepareDeleteUser(ctx, userId)
	if err != nil {
		logger.L().Error(ctx, "failed to prepare user deletion", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to prepare user deletion")

		return
	}

	// Delete from public.users (cascades to user_identities via FK).
	// Done before the IdP removal so a DB failure does not orphan the identity.
	if err := s.authDB.Write.DeletePublicUser(ctx, userId); err != nil {
		logger.L().Error(ctx, "failed to delete public user", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete public user record")

		return
	}

	// Remove the external identity (e.g. Ory) using pre-fetched references.
	if err := handle.Execute(ctx); err != nil {
		logger.L().Error(ctx, "failed to delete user identity provider record", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete user identity provider record")

		return
	}

	c.Status(http.StatusNoContent)
}
