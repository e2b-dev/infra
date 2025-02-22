package handlers

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

func (a *APIStore) GetUserID(c *gin.Context) uuid.UUID {
	return c.Value(auth.UserIDContextKey).(uuid.UUID)
}

func (a *APIStore) GetUserAndTeams(c *gin.Context) (*uuid.UUID, []*models.Team, error) {
	userID := a.GetUserID(c)
	ctx := c.Request.Context()

	teams, err := a.db.GetTeams(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("error when getting default team: %w", err)
	}

	return &userID, teams, err
}

func (a *APIStore) GetTeamInfo(c *gin.Context) authcache.AuthTeamInfo {
	return c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
}
