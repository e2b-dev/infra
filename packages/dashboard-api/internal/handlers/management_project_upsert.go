package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// ManagementUpsertProject creates or reconciles a project's control-plane state.
func (s *APIStore) ManagementUpsertProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}
