package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func (a *APIStore) GetUserID(c *gin.Context) uuid.UUID {
	return c.Value(auth.UserIDContextKey).(uuid.UUID)
}

func (a *APIStore) GetUserAndTeams(c *gin.Context) (*uuid.UUID, []queries.GetTeamsWithUsersTeamsWithTierRow, error) {
	userID := a.GetUserID(c)
	ctx := c.Request.Context()

	teams, err := a.sqlcDB.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("error when getting default team: %w", err)
	}

	return &userID, teams, err
}

func (a *APIStore) GetTeamInfo(c *gin.Context) authcache.AuthTeamInfo {
	return c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
}

func (a *APIStore) GetTeamAndTier(
	c *gin.Context,
	// Deprecated: use API Token authentication instead.
	teamID *string,
) (*queries.Team, *queries.Tier, *api.APIError) {
	_, span := tracer.Start(c.Request.Context(), "get-team-and-tier")
	defer span.End()

	if c.Value(auth.TeamContextKey) != nil {
		teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

		return teamInfo.Team, teamInfo.Tier, nil
	} else if c.Value(auth.UserIDContextKey) != nil {
		_, teams, err := a.GetUserAndTeams(c)
		if err != nil {
			return nil, nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error when getting user and teams",
				Err:       err,
			}
		}
		team, tier, err := findTeamAndTier(teams, teamID)
		if err != nil {
			if teamID == nil {
				return nil, nil, &api.APIError{
					Code:      http.StatusInternalServerError,
					ClientMsg: "Default team not found",
					Err:       err,
				}
			}

			return nil, nil, &api.APIError{
				Code:      http.StatusForbidden,
				ClientMsg: "You are not allowed to access this team",
				Err:       err,
			}
		}

		return team, tier, nil
	}

	return nil, nil, &api.APIError{
		Code:      http.StatusUnauthorized,
		ClientMsg: "You are not authenticated",
		Err:       errors.New("invalid authentication context for team and tier"),
	}
}

// findTeamAndTier finds the appropriate team and tier based on the provided teamID or returns the default team
func findTeamAndTier(teams []queries.GetTeamsWithUsersTeamsWithTierRow, teamID *string) (*queries.Team, *queries.Tier, error) {
	if teamID != nil {
		teamUUID, err := uuid.Parse(*teamID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid team ID: %s", *teamID)
		}

		for _, t := range teams {
			if t.Team.ID == teamUUID {
				return &t.Team, &t.Tier, nil
			}
		}

		return nil, nil, fmt.Errorf("team '%s' not found", *teamID)
	}

	// Find default team
	for _, t := range teams {
		if t.UsersTeam.IsDefault {
			return &t.Team, &t.Tier, nil
		}
	}

	return nil, nil, fmt.Errorf("default team not found")
}
