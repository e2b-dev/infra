package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	team_ "github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

type ForbiddenErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func TestBannedTeam(t *testing.T) {
	ctx := t.Context()
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	teamName := "test-team-banned"
	teamID := utils.CreateTeamWithUser(t, c, db, teamName, setup.UserID)
	apiKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, teamID)

	err := db.Client.Team.UpdateOneID(teamID).SetIsBanned(true).Exec(ctx)
	require.NoError(t, err)

	team, err := db.Client.Team.Query().Where(team_.ID(teamID)).First(ctx)
	require.NoError(t, err)

	assert.True(t, team.IsBanned)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	require.NoError(t, err)

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "forbidden: error while getting the team: team is banned", errResp.Message)
}

func TestBlockedTeam(t *testing.T) {
	ctx := t.Context()
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	teamName := "test-team-blocked"
	blockReason := "test-reason"

	teamID := utils.CreateTeamWithUser(t, c, db, teamName, setup.UserID)
	apiKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, teamID)

	err := db.Client.Team.UpdateOneID(teamID).SetIsBlocked(true).SetBlockedReason(blockReason).Exec(ctx)
	require.NoError(t, err)

	team, err := db.Client.Team.Query().Where(team_.ID(teamID)).First(ctx)
	require.NoError(t, err)

	assert.True(t, team.IsBlocked)
	assert.Equal(t, teamID, team.ID)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	require.NoError(t, err)

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "blocked: error while getting the team: team is blocked", errResp.Message)
}
