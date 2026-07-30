package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ManagementPurgeUser removes the membership and access-token state a user
// holds on this cluster.
//
// The caller fans this out to every control plane it knows, so a user with
// nothing here is ordinary rather than an error — hence no 404 in the contract
// and none returned.
func (s *APIStore) ManagementPurgeUser(c *gin.Context, userID api.UserId) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithUserID(userID.String())}

	if err := s.managementService.PurgeUser(ctx, userID); err != nil {
		telemetry.ReportCriticalError(ctx, "purge user failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error purging user")

		return
	}

	c.Status(http.StatusNoContent)
}
