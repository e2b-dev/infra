package utils

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func CreateTeam(t *testing.T, db *setup.Database, teamName string) uuid.UUID {
	t.Helper()

	return CreateTeamWithUser(t, db, teamName, "")
}

func CreateTeamWithUser(
	t *testing.T,
	db *setup.Database,
	teamName, userID string,
) uuid.UUID {
	t.Helper()

	teamID := uuid.New()
	slug := fmt.Sprintf("test-%s", teamID.String()[:8])

	err := db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO teams (id, email, name, tier, is_blocked, slug)
VALUES ($1, $2, $3, $4, $5, $6)
`, teamID, fmt.Sprintf("test-integration-%s@e2b.dev", teamID), teamName, "base_v1", false, slug)
	require.NoError(t, err)

	if userID != "" {
		AddUserToTeam(t, db, teamID, userID)
	}

	t.Cleanup(func() {
		db.AuthDb.TestsRawSQL(t.Context(), `
DELETE FROM teams WHERE id = $1
`, teamID)
	})

	return teamID
}

func AddUserToTeam(t *testing.T, db *setup.Database, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	err = db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
`, userUUID, teamID, false)
	require.NoError(t, err)

	t.Cleanup(func() {
		db.AuthDb.TestsRawSQL(t.Context(), `
DELETE FROM users_teams WHERE user_id = $1 and team_id = $2
`, userUUID, teamID)
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
