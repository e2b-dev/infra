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

func CreateTeam(t *testing.T, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamName string) uuid.UUID {
	t.Helper()

	return CreateTeamWithUser(t, ctx, c, db, teamName, "")
}

func CreateTeamWithUser(
	t *testing.T,
	ctx context.Context,
	c *api.ClientWithResponses,
	db *db.DB,
	teamName string,
	userID string,
) uuid.UUID {
	t.Helper()

	teamID := uuid.New()

	team, err := db.Client.Team.Create().SetID(teamID).SetEmail(fmt.Sprintf("test-integration-%s@e2b.dev", teamID)).SetName(teamName).SetTier("base_v1").Save(ctx)
	require.NoError(t, err)

	assert.Equal(t, teamName, team.Name)
	assert.Equal(t, teamID, team.ID)

	if userID != "" {
		AddUserToTeam(t, ctx, c, db, teamID, userID)
	}

	// Cleanup should use background context as test context may be canceled
	//nolint:contextcheck
	t.Cleanup(func() {
		db.Client.Team.DeleteOneID(teamID).Exec(context.Background())
		db.Client.TeamAPIKey.DeleteOneID(teamID).Exec(context.Background())
	})

	return team.ID
}

func AddUserToTeam(t *testing.T, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	userTeam, err := db.Client.UsersTeams.Create().
		SetUserID(userUUID).
		SetTeamID(teamID).
		SetIsDefault(false).
		Save(ctx)
	require.NoError(t, err)

	// Cleanup should use background context as test context may be canceled
	//nolint:contextcheck
	t.Cleanup(func() {
		db.Client.UsersTeams.DeleteOne(userTeam).Exec(context.Background())
	})
}

func RemoveUserFromTeam(t *testing.T, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID string) {
	t.Helper()

	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	_, err = db.Client.UsersTeams.Delete().
		Where(usersteams.UserID(userUUID), usersteams.TeamID(teamID)).
		Exec(ctx)
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
