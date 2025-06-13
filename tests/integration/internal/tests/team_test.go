package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	team_ "github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

type ForbiddenErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func createTeam(t *testing.T, cancel context.CancelFunc, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, teamName string) (*models.Team, string) {
	// Create team
	team, err := db.Client.Team.Create().SetID(teamID).SetEmail(fmt.Sprintf("test-integration-%s@e2b.dev", teamID)).SetName(teamName).SetTier("base_v1").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, teamName, team.Name)
	assert.Equal(t, teamID, team.ID)

	userID := uuid.MustParse(os.Getenv("TESTS_SANDBOX_USER_ID"))
	userTeam, err := db.Client.UsersTeams.Create().
		SetUserID(userID).
		SetTeamID(teamID).
		SetIsDefault(true).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: fmt.Sprintf("test-%s", teamID),
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID.String()))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	apiKey := resp.JSON201.Key

	t.Cleanup(func() {
		db.Client.UsersTeams.DeleteOne(userTeam)
		db.Client.Team.DeleteOneID(teamID).Exec(ctx)
		db.Client.TeamAPIKey.DeleteOneID(teamID).Exec(ctx)
		cancel()
		db.Close()
	})

	return team, apiKey
}

func TestBannedTeam(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db := setup.GetTestDBClient()
	c := setup.GetAPIClient()

	teamID := uuid.MustParse("db4da60a-d4ca-424d-af78-7aebd019a0e6")
	teamName := "test-team-banned"

	_, apiKey := createTeam(t, cancel, ctx, c, db, teamID, teamName)

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
	ctx, cancel := context.WithCancel(context.Background())
	db := setup.GetTestDBClient()
	c := setup.GetAPIClient()

	teamID := uuid.MustParse("ba400701-c2ca-42f5-913a-2067998dd001")
	teamName := "test-team-blocked"
	blockReason := "test-reason"

	_, apiKey := createTeam(t, cancel, ctx, c, db, teamID, teamName)

	err := db.Client.Team.UpdateOneID(teamID).SetIsBlocked(true).SetBlockedReason(blockReason).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	team, err := db.Client.Team.Query().Where(team_.ID(teamID)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.True(t, team.IsBlocked)
	assert.Equal(t, blockReason, team.BlockedReason)
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
