package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

func (s *APIStore) ManagementUpsertProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) ManagementDeleteProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) ManagementUpsertProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}

func (s *APIStore) ManagementDeleteProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}

func (s *APIStore) ManagementBatchSyncProjectMembers(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) ManagementPurgeUser(c *gin.Context, _ api.UserId) {
	sendNotImplemented(c)
}

func sendNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, api.Error{
		Code:    http.StatusNotImplemented,
		Message: "operation is not implemented",
	})
}
