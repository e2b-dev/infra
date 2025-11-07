package utils

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func CreateTeam(t *testing.T, sqlcDB *client.Client, teamName string) uuid.UUID {
	t.Helper()

	return CreateTeamWithUser(t, sqlcDB, teamName, "")
}

func CreateTeamWithUser(
	t *testing.T,
	sqlcDB *client.Client,
	teamName, userID string,
) uuid.UUID {
	t.Helper()

	teamID := uuid.New()

	err := sqlcDB.TestsRawSQL(t.Context(), `
INSERT INTO teams (id, email, name, tier, is_blocked)
VALUES ($1, $2, $3, $4, $5)
`, teamID, fmt.Sprintf("test-integration-%s@e2b.dev", teamID), teamName, "base_v1", false)
	require.NoError(t, err)

	if userID != "" {
		AddUserToTeam(t, sqlcDB, teamID, userID)
	}

	t.Cleanup(func() {
		sqlcDB.TestsRawSQL(t.Context(), `
DELETE FROM teams WHERE id = $1
`, teamID)
	})

	return teamID
}

func AddUserToTeam(t *testing.T, sqlcDB *client.Client, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	var userTeamID int64
	err = sqlcDB.TestsRawSQL(t.Context(), `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
RETURNING id
`, userUUID, teamID, false)
	require.NoError(t, err)

	t.Cleanup(func() {
		if userTeamID != 0 {
			sqlcDB.TestsRawSQL(t.Context(), `
DELETE FROM users_teams WHERE id = $1
`, userTeamID)
		}
	})
}

func CreateAPIKey(t *testing.T, ctx context.Context, c *api.ClientWithResponses, userID string, teamID uuid.UUID) string {
	t.Helper()

	apiKey, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: uuid.New().String(),
	}, setup.WithSupabaseToken(t, userID), setup.WithSupabaseTeam(t, teamID.String()))
	require.NoError(t, err)
	require.NotNil(t, apiKey.JSON201)

	t.Cleanup(func() {
		_, _ = c.DeleteApiKeysApiKeyIDWithResponse(
			ctx,
			apiKey.JSON201.Id.String(),
			setup.WithSupabaseToken(t, userID),
			setup.WithSupabaseTeam(t, teamID.String()),
		)
	})

	return apiKey.JSON201.Key
}
