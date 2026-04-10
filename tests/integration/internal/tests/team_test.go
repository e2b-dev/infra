package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

type ForbiddenErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func TestBannedTeam(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	teamName := "test-team-banned"
	teamID := utils.CreateTeamWithUser(t, db, teamName, setup.UserID)
	apiKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, teamID)

	err := db.AuthDb.TestsRawSQL(ctx, `
UPDATE teams SET is_banned = $1 WHERE id = $2
`, true, teamID)
	require.NoError(t, err)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	require.NoError(t, err)

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "forbidden: team is banned", errResp.Message)
}

func TestBlockedTeam(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	teamName := "test-team-blocked"
	blockReason := "test-reason"

	teamID := utils.CreateTeamWithUser(t, db, teamName, setup.UserID)
	apiKey := utils.CreateAPIKey(t, ctx, c, setup.UserID, teamID)

	err := db.AuthDb.TestsRawSQL(ctx, `
UPDATE teams SET is_blocked = $1, blocked_reason = $2 WHERE id = $3
`, true, blockReason, teamID)
	require.NoError(t, err)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	require.NoError(t, err)

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "blocked: team is blocked", errResp.Message)
}
