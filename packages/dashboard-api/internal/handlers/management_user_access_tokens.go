package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) ManagementPurgeUserAccessTokens(c *gin.Context, userID api.UserID) {
	ctx := c.Request.Context()
	if err := s.managementService.PurgeUserAccessTokens(ctx, userID); err != nil {
		telemetry.ReportCriticalError(ctx, "purge user access tokens failed", err, telemetry.WithUserID(userID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error revoking user access tokens")

		return
	}

	c.Status(http.StatusNoContent)
}
