package db

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// The deprecated access-token path picks a team from this list, so a deleted
// team must not be in it even though its membership is kept.
func TestGetTeamsByUserLeavesOutADeletedTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	userID := uuid.New()
	require.NoError(t, db.AuthDB.UpsertPublicUser(t.Context(), userID))
	deletedTeam := testutils.CreateTestTeam(t, db)
	liveTeam := testutils.CreateTestTeam(t, db)
	for _, teamID := range []uuid.UUID{deletedTeam, liveTeam} {
		require.NoError(t, db.AuthDB.CreateTeamMembership(t.Context(), authqueries.CreateTeamMembershipParams{
			UserID: userID, TeamID: teamID, IsDefault: teamID == deletedTeam,
		}))
	}
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		"UPDATE public.teams SET deleted_at = now(), is_blocked = true WHERE id = $1", deletedTeam))

	teams, err := GetTeamsByUser(t.Context(), db.AuthDB, userID)

	require.NoError(t, err)
	require.Len(t, teams, 1)
	require.Equal(t, liveTeam, teams[0].Team.ID)
}
