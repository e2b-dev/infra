package handlers

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
)

var ErrNoUserIDInContext = errors.New("no user id in context")

func (a *APIStore) GetUserAndTeams(c *gin.Context) (*uuid.UUID, []queries.GetTeamsWithUsersTeamsWithTierRow, error) {
	userID, err := auth.GetUserID(c)
	if err != nil {
		return nil, nil, ErrNoUserIDInContext
	}

	ctx := c.Request.Context()

	teams, err := a.sqlcDB.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("error when getting default team: %w", err)
	}

	return &userID, teams, err
}
