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
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
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
	ctx, span := tracer.Start(ctx, "get team-and-tier")
	defer span.End()

	switch {
	case c.Value(auth.TeamContextKey) != nil:
		return a.GetTeamInfo(c), nil
	case c.Value(auth.UserIDContextKey) != nil:
		teams, apiErr := a.getUserTeams(ctx, c)
		if apiErr != nil {
			return nil, apiErr
		}

		team, err := findTeam(teams, teamID)
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
			Err:       errors.New("invalid authentication context"),
		}
	}
}

func findTeam(teams []*types.TeamWithDefault, teamID *string) (*types.Team, error) {
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

	for _, t := range teams {
		if t.IsDefault {
			return t.Team, nil
		}
	}

	return nil, fmt.Errorf("default team not found")
}

func (a *APIStore) getUserTeams(ctx context.Context, c *gin.Context) ([]*types.TeamWithDefault, *api.APIError) {
	teams, err := dbapi.GetTeamsByUser(ctx, a.authDB, a.GetUserID(c))
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error getting user teams",
			Err:       err,
		}
	}

	if len(teams) == 0 {
		return nil, &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: "You don't have access to any teams",
			Err:       errors.New("user has no teams"),
		}
	}

	return teams, nil
}

// resolveTemplateAndTeam resolves a template identifier and returns both the alias info and the owning team.
// For API key auth: supports both template ID and alias (team context is unambiguous).
// For access token auth: only template ID lookup (aliases are ambiguous across multiple teams).
// Returns 403 if the template is found but user doesn't have ownership.
func (a *APIStore) resolveTemplateAndTeam(
	ctx context.Context,
	c *gin.Context,
	identifier string,
) (*types.Team, *templatecache.AliasInfo, *api.APIError) {
	switch {
	case c.Value(auth.TeamContextKey) != nil:
		team := a.GetTeamInfo(c)
		aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, team.Slug)
		if err != nil {
			return nil, nil, templatecache.ErrorToAPIError(err, identifier)
		}

		if aliasInfo.TeamID != team.ID {
			return nil, nil, &api.APIError{
				Code:      http.StatusForbidden,
				ClientMsg: fmt.Sprintf("You don't have access to template '%s'", identifier),
				Err:       fmt.Errorf("team '%s' does not own template", team.ID),
			}
		}

		return team, aliasInfo, nil
	case c.Value(auth.UserIDContextKey) != nil:
		aliasInfo, err := a.templateCache.GetByID(ctx, identifier)
		if err != nil {
			return nil, nil, templatecache.ErrorToAPIError(err, identifier)
		}

		userTeams, apiErr := a.getUserTeams(ctx, c)
		if apiErr != nil {
			return nil, nil, apiErr
		}

		for _, t := range userTeams {
			if t.Team.ID == aliasInfo.TeamID {
				return t.Team, aliasInfo, nil
			}
		}

		return nil, nil, &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: fmt.Sprintf("You don't have access to template '%s'", identifier),
			Err:       fmt.Errorf("user does not have access to template's team"),
		}
	default:
		return nil, nil, &api.APIError{
			Code:      http.StatusUnauthorized,
			ClientMsg: "You are not authenticated",
			Err:       errors.New("invalid authentication context"),
		}
	}
}
