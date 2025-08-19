package utils

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func CreateTeam(t *testing.T, cancel context.CancelFunc, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, teamName string) (*models.Team, string) {
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
		SetIsDefault(false).
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
	require.Equal(t, http.StatusCreated, resp.StatusCode())
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
