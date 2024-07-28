package handlers

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

func (a *APIStore) GetUserID(c *gin.Context) uuid.UUID {
	return c.Value(auth.UserIDContextKey).(uuid.UUID)
}

func (a *APIStore) GetTeams(c *gin.Context) ([]*models.Team, error) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	teams, err := a.db.GetTeams(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("error when getting default team: %w", err)
	}

	return teams, nil
}

func (a *APIStore) GetUserAndTeams(c *gin.Context) (*uuid.UUID, []*models.Team, error) {
	teams, err := a.GetTeams(c)
	if err != nil {
		return nil, nil, err
	}

	userID := a.GetUserID(c)
	return &userID, teams, err
}
