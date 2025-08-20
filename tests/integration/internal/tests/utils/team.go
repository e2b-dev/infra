package utils

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/usersteams"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

func CreateTeam(t *testing.T, cancel context.CancelFunc, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, teamName string) *models.Team {
	return CreateTeamWithUser(t, cancel, ctx, c, db, teamID, teamName, uuid.Nil)
}

func CreateTeamWithUser(t *testing.T, cancel context.CancelFunc, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, teamName string, userID uuid.UUID) *models.Team {
	// Create team
	team, err := db.Client.Team.Create().SetID(teamID).SetEmail(fmt.Sprintf("test-integration-%s@e2b.dev", teamID)).SetName(teamName).SetTier("base_v1").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, teamName, team.Name)
	assert.Equal(t, teamID, team.ID)

	if userID != uuid.Nil {
		AddUserToTeam(t, ctx, c, db, teamID, userID)
	}

	t.Cleanup(func() {
		db.Client.Team.DeleteOneID(teamID).Exec(ctx)
		db.Client.TeamAPIKey.DeleteOneID(teamID).Exec(ctx)
		cancel()
		db.Close()
	})

	return team
}

func AddUserToTeam(t *testing.T, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID uuid.UUID) {
	userTeam, err := db.Client.UsersTeams.Create().
		SetUserID(userID).
		SetTeamID(teamID).
		SetIsDefault(false).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		db.Client.UsersTeams.DeleteOne(userTeam).Exec(ctx)
	})
}

func RemoveUserFromTeam(t *testing.T, ctx context.Context, c *api.ClientWithResponses, db *db.DB, teamID uuid.UUID, userID uuid.UUID) {
	_, err := db.Client.UsersTeams.Delete().
		Where(usersteams.UserID(userID), usersteams.TeamID(teamID)).
		Exec(ctx)
	require.NoError(t, err, "failed to remove user from team")
}
