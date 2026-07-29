package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementPurgeUser purges shard-local membership and access-token state for an opaque user UUID.
func (s *APIStore) ManagementPurgeUser(c *gin.Context, _ api.UserId) {
	sendNotImplemented(c)
}
