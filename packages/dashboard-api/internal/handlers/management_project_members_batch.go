package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementBatchSyncProjectMembers reconciles many project memberships in one call, for group and directory fan-outs.
func (s *APIStore) ManagementBatchSyncProjectMembers(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}
