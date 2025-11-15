package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
)

func (a *APIStore) GetUserID(c *gin.Context) uuid.UUID {
	return c.Value(auth.UserIDContextKey).(uuid.UUID)
}

func (a *APIStore) GetTeamInfo(c *gin.Context) *types.Team {
	return c.Value(auth.TeamContextKey).(*types.Team)
}

func (a *APIStore) GetTeam(
	ctx context.Context,
	c *gin.Context,
	// Deprecated: use API Token authentication instead.
	teamID *string,
) (*types.Team, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get-team-and-tier")
	defer span.End()

	switch {
	case c.Value(auth.TeamContextKey) != nil:
		teamInfo := c.Value(auth.TeamContextKey).(*types.Team)

		return teamInfo, nil
	case c.Value(auth.UserIDContextKey) != nil:
		userID := a.GetUserID(c)
		teams, err := dbapi.GetTeamsByUser(ctx, a.sqlcDB, userID)
		if err != nil {
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error when getting user and teams",
				Err:       err,
			}
		}

		team, err := findTeamAndLimits(teams, teamID)
		if err != nil {
			if teamID == nil {
				return nil, &api.APIError{
					Code:      http.StatusInternalServerError,
					ClientMsg: "Default team not found",
					Err:       err,
				}
			}

			return nil, &api.APIError{
				Code:      http.StatusForbidden,
				ClientMsg: "You are not allowed to access this team",
				Err:       err,
			}
		}

		return team, nil
	default:
		return nil, &api.APIError{
			Code:      http.StatusUnauthorized,
			ClientMsg: "You are not authenticated",
			Err:       errors.New("invalid authentication context for team and tier"),
		}
	}
}

// findTeamAndTier finds the appropriate team and limits based on the provided teamID or returns the default team
func findTeamAndLimits(teams []*types.TeamWithDefault, teamID *string) (*types.Team, error) {
	if teamID != nil {
		teamUUID, err := uuid.Parse(*teamID)
		if err != nil {
			return nil, fmt.Errorf("invalid team ID: %s", *teamID)
		}

		for _, t := range teams {
			if t.Team.ID == teamUUID {
				return t.Team, nil
			}
		}

		return nil, fmt.Errorf("team '%s' not found", *teamID)
	}

	// Find default team
	for _, t := range teams {
		if t.IsDefault {
			return t.Team, nil
		}
	}

	return nil, fmt.Errorf("default team not found")
}
