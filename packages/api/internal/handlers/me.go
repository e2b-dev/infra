package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

func (a *APIStore) GetMe(c *gin.Context) {
	teamInfo, ok := c.Value(auth.TeamContextKey).(*types.Team)
	if !ok || teamInfo == nil {
		a.sendAPIStoreError(c, http.StatusUnauthorized, "no credentials found")

		return
	}

	tokenInfo := api.TokenInfo{
		TeamID:   teamInfo.ID,
		TeamName: teamInfo.Name,
	}

	c.JSON(200, tokenInfo)
}
