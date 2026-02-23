package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) GetMe(c *gin.Context) {
	var tokenInfo api.TokenInfo

	teamInfo := a.GetTeamInfo(c)
	userID := a.GetUserID(c)

	switch {
	case teamInfo != nil:
		tokenInfo.TeamID = &teamInfo.ID
		tokenInfo.TeamName = &teamInfo.Name
	case userID != uuid.Nil:
		tokenInfo.UserID = &userID
	default:
		a.sendAPIStoreError(c, http.StatusUnauthorized, "no credentials found")

		return
	}

	c.JSON(200, tokenInfo)
}
