package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

func (a *APIStore) GetMe(c *gin.Context) {
	var tokenInfo api.TokenInfo

	teamInfo, teamOK := c.Value(auth.TeamContextKey).(*types.Team)
	userID, userOK := c.Value(auth.UserIDContextKey).(uuid.UUID)

	switch {
	case teamOK && teamInfo != nil:
		tokenInfo.TeamID = &teamInfo.ID
		tokenInfo.TeamName = &teamInfo.Name
	case userOK && userID != uuid.Nil:
		tokenInfo.UserID = &userID
	default:
		a.sendAPIStoreError(c, http.StatusUnauthorized, "no credentials found")

		return
	}

	c.JSON(200, tokenInfo)
}
