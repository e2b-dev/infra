package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

func (s *APIStore) UpsertProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) DeleteProject(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) UpsertProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}

func (s *APIStore) DeleteProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	sendNotImplemented(c)
}

func (s *APIStore) UpsertProjectLimits(c *gin.Context, _ api.TeamID) {
	sendNotImplemented(c)
}

func (s *APIStore) PurgeUser(c *gin.Context, _ api.UserId) {
	sendNotImplemented(c)
}

func sendNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, api.Error{
		Code:    http.StatusNotImplemented,
		Message: "operation is not implemented",
	})
}
