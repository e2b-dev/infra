package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetMe(c *gin.Context) {
	teamInfo := a.GetTeamInfo(c)
	if teamInfo == nil {
		a.sendAPIStoreError(c, http.StatusUnauthorized, "no credentials found")

		return
	}

	tokenInfo := api.TokenInfo{
		TeamID:   &teamInfo.ID,
		TeamName: &teamInfo.Name,
	}

	c.JSON(200, tokenInfo)
}
