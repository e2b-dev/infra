package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetMe(c *gin.Context) {
	teamInfo, ok := a.safeGetTeamInfo(c)
	if !ok {
		a.sendAPIStoreError(c, http.StatusUnauthorized, "no credentials found")

		return
	}

	tokenInfo := api.TokenInfo{
		TeamID:   teamInfo.ID,
		TeamName: teamInfo.Name,
	}

	c.JSON(200, tokenInfo)
}
