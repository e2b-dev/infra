package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

func (a *APIStore) GetMe(c *gin.Context) {
	teamInfo := auth.MustGetTeamInfo(c)

	tokenInfo := api.TokenInfo{
		TeamID:   teamInfo.ID,
		TeamName: teamInfo.Name,
	}

	c.JSON(200, tokenInfo)
}
