package utils

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/usersteams"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func CreateTeam(t *testing.T, c *api.ClientWithResponses, db *db.DB, teamName string) uuid.UUID {
	t.Helper()

	return CreateTeamWithUser(t, c, db, teamName, "")
}

func CreateTeamWithUser(
	t *testing.T,
	c *api.ClientWithResponses,
	db *db.DB,
	teamName string,
	userID string,
) uuid.UUID {
	t.Helper()

	teamID := uuid.New()

	team, err := db.Client.Team.Create().SetID(teamID).SetEmail(fmt.Sprintf("test-integration-%s@e2b.dev", teamID)).SetName(teamName).SetTier("base_v1").Save(t.Context())
	require.NoError(t, err)

	assert.Equal(t, teamName, team.Name)
	assert.Equal(t, teamID, team.ID)

	if userID != "" {
		AddUserToTeam(t, c, db, teamID, userID)
	}

	t.Cleanup(func() {
		db.Client.Team.DeleteOneID(teamID).Exec(t.Context())
		db.Client.TeamAPIKey.DeleteOneID(teamID).Exec(t.Context())
	})

	return team.ID
}

func AddUserToTeam(t *testing.T, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	userTeam, err := db.Client.UsersTeams.Create().
		SetUserID(userUUID).
		SetTeamID(teamID).
		SetIsDefault(false).
		Save(t.Context())
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Client.UsersTeams.DeleteOne(userTeam).Exec(t.Context())
	})
}

func RemoveUserFromTeam(t *testing.T, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	_, err = db.Client.UsersTeams.Delete().
		Where(usersteams.UserID(userUUID), usersteams.TeamID(teamID)).
		Exec(t.Context())
	require.NoError(t, err, "failed to remove user from team")
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
