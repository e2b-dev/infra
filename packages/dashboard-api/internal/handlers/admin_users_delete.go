package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const identityDeleteMaxRetries = 3

func (s *APIStore) DeleteAdminUsersUserId(c *gin.Context, userId api.UserId) {
	ctx := c.Request.Context()

	// Resolve the external identity references while user_identities still exists.
	handle, err := s.userProfiles.PrepareDeleteUser(ctx, userId)
	if err != nil {
		if errors.Is(err, userprofile.ErrUserNotFound) {
			s.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("User %s not found or has no identity provider record", userId))

			return
		}

		logger.L().Error(ctx, "failed to prepare user deletion", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to resolve identity provider record for user")

		return
	}

	// Delete from public.users (cascades to user_identities via FK).
	// Done before the IdP removal so a DB failure does not orphan the identity.
	if err := s.authDB.Write.DeletePublicUser(ctx, userId); err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("User %s not found", userId))

			return
		}

		if dberrors.IsForeignKeyViolation(err) {
			logger.L().Warn(ctx, "cannot delete user due to existing references", zap.String("user_id", userId.String()), zap.Error(err))
			s.sendAPIStoreError(c, http.StatusConflict, "Cannot delete user: existing references (e.g. addons) must be removed first")

			return
		}

		logger.L().Error(ctx, "failed to delete public user", zap.String("user_id", userId.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete public user record")

		return
	}

	// Remove the external identity (e.g. Ory) using pre-fetched references.
	// Retry since the DB rows are already gone and we must not leave the IdP identity active.
	// Use a detached context so a client disconnect does not cancel the cleanup.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cleanupCancel()

	var identityErr error
	for attempt := range identityDeleteMaxRetries {
		identityErr = handle.Execute(cleanupCtx)
		if identityErr == nil {
			break
		}

		logger.L().Warn(ctx, "retrying identity deletion",
			zap.String("user_id", userId.String()),
			zap.Int("attempt", attempt+1),
			zap.Error(identityErr),
		)

		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}

	if identityErr != nil {
		logger.L().Error(ctx, "failed to delete user identity provider record after retries", zap.String("user_id", userId.String()), zap.Error(identityErr))
		s.sendAPIStoreError(c, http.StatusInternalServerError,
			"User DB records deleted but identity provider removal failed — the IdP identity may need manual cleanup")

		return
	}

	c.Status(http.StatusNoContent)
}
