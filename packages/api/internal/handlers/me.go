package handlers

import (
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (a *APIStore) GetMe(c *gin.Context) {
	ctx := c.Request.Context()

	var tokenInfo api.TokenInfo

	teamInfo, teamOK := c.Value(auth.TeamContextKey).(*types.Team)
	userID, userOK := c.Value(auth.UserIDContextKey).(uuid.UUID)

	if !(teamOK || userOK) {
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}

	if teamOK && teamInfo != nil {
		tokenInfo.TeamID = &teamInfo.ID
		tokenInfo.TeamName = &teamInfo.Name
	}

	if userOK && userID != uuid.Nil {
		tokenInfo.UserID = &userID
	}

	c.JSON(200, tokenInfo)
}
