package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementDeleteProject deletes a project and the control-plane state belonging to it.
func (s *APIStore) ManagementDeleteProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}
