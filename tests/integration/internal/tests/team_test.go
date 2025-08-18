package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	team_ "github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/tests/utils"
)

type ForbiddenErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func TestBannedTeam(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	db := setup.GetTestDBClient()
	c := setup.GetAPIClient()

	teamID := uuid.MustParse("db4da60a-d4ca-424d-af78-7aebd019a0e6")
	teamName := "test-team-banned"

	_, apiKey := utils.CreateTeam(t, cancel, ctx, c, db, teamID, teamName)

	err := db.Client.Team.UpdateOneID(teamID).SetIsBanned(true).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	team, err := db.Client.Team.Query().Where(team_.ID(teamID)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.True(t, team.IsBanned)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	if err != nil {
		t.Fatal(err)
	}

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "forbidden: error while getting the team: team is banned", errResp.Message)
}

func TestBlockedTeam(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	db := setup.GetTestDBClient()
	c := setup.GetAPIClient()

	teamID := uuid.MustParse("ba400701-c2ca-42f5-913a-2067998dd001")
	teamName := "test-team-blocked"
	blockReason := "test-reason"

	_, apiKey := utils.CreateTeam(t, cancel, ctx, c, db, teamID, teamName)

	err := db.Client.Team.UpdateOneID(teamID).SetIsBlocked(true).SetBlockedReason(blockReason).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	team, err := db.Client.Team.Query().Where(team_.ID(teamID)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.True(t, team.IsBlocked)
	assert.Equal(t, teamID, team.ID)

	resp, err := c.GetSandboxesWithResponse(ctx, nil, setup.WithAPIKey(apiKey))
	if err != nil {
		t.Fatal(err)
	}

	var errResp ForbiddenErrorResponse
	err = json.Unmarshal(resp.Body, &errResp)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusForbidden, resp.StatusCode())
	assert.Equal(t, http.StatusForbidden, errResp.Code)
	assert.Equal(t, "blocked: error while getting the team: team is blocked", errResp.Message)
}
