package db

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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

// One statement returns each team with its limits: the pushed row, else the
// deployment's defaults.
func TestGetTeamsByUserResolvesEachTeamsLimitsInOneStatement(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	userID := uuid.New()
	require.NoError(t, db.AuthDB.UpsertPublicUser(t.Context(), userID))
	pushed := testutils.CreateTestTeam(t, db)
	unpushed := testutils.CreateTestTeam(t, db)
	for _, teamID := range []uuid.UUID{pushed, unpushed} {
		require.NoError(t, db.AuthDB.CreateTeamMembership(t.Context(), authqueries.CreateTeamMembershipParams{
			UserID: userID, TeamID: teamID, IsDefault: teamID == unpushed,
		}))
	}
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(), `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets
		) VALUES ($1, 1, 222, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)`, pushed))
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`UPDATE public.default_limits SET concurrent_sandboxes = 33`))

	recordedDB, recorder := db.RecordedAuthDB(t)

	teams, err := GetTeamsByUser(t.Context(), recordedDB, userID)

	require.NoError(t, err)
	require.Equal(t, []string{"GetUserTeamsWithLimits"}, recorder.Take())
	sandboxes := map[uuid.UUID]int64{}
	defaults := map[uuid.UUID]bool{}
	for _, team := range teams {
		sandboxes[team.Team.ID] = team.Limits.SandboxConcurrency
		defaults[team.Team.ID] = team.IsDefault
	}
	require.Equal(t, map[uuid.UUID]int64{pushed: 222, unpushed: 33}, sandboxes)
	require.Equal(t, map[uuid.UUID]bool{pushed: false, unpushed: true}, defaults)
}

// A column added to both rows but not to teamRow would reach the team as zero.
func TestTeamRowCopiesEveryColumn(t *testing.T) {
	t.Parallel()

	var source authqueries.GetUserTeamsWithLimitsRow
	value := reflect.ValueOf(&source).Elem()
	for i := range value.NumField() {
		if field := value.Field(i); field.Kind() == reflect.Int64 {
			field.SetInt(int64(i + 1))
		}
	}
	source.Team.ID = uuid.New()

	copied := reflect.ValueOf(teamRow(source))
	for i := range copied.NumField() {
		name := copied.Type().Field(i).Name
		assert.Equal(t, value.FieldByName(name).Interface(), copied.Field(i).Interface(), name)
	}
}
