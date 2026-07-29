package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementUpsertProjectMember reconciles an opaque user UUID as a member of a project.
func (s *APIStore) ManagementUpsertProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}
