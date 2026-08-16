package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetAdminSandboxesRunningCounts(c *gin.Context) {
	counts, err := a.teamSandboxCounter.TeamRunningSandboxCounts(c.Request.Context())
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to count running sandboxes")

		return
	}

	response := make(api.AdminTeamRunningSandboxCounts, len(counts))
	for teamID, count := range counts {
		response[teamID.String()] = count
	}

	c.JSON(http.StatusOK, response)
}
