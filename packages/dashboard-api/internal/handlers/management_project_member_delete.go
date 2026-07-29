package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementDeleteProjectMember removes a project member.
func (s *APIStore) ManagementDeleteProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}
