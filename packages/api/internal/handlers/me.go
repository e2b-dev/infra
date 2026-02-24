package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetMe(c *gin.Context) {
	teamInfo, _ := a.safeGetTeamInfo(c)

	tokenInfo := api.TokenInfo{
		TeamID:   teamInfo.ID,
		TeamName: teamInfo.Name,
	}

	c.JSON(200, tokenInfo)
}
