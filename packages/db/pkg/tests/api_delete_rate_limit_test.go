package tests

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbmodule "github.com/e2b-dev/infra/packages/db"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestAPIDeleteRateResolvesTierAndActiveAddons(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	teamID := seedTeam(t, sqlDB, "delete-rate")
	otherTeamID := seedTeam(t, sqlDB, "other-delete-rate")
	resolved := func(id uuid.UUID) int64 {
		t.Helper()
		row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), id)
		require.NoError(t, err)

		return row.TeamLimit.ApiTeamRpsDelete
	}
	assert.Zero(t, resolved(teamID))
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_delete = 5, api_team_rps_list = 13 WHERE id = 'base_v1'`)
	require.NoError(t, err)
	assert.Equal(t, int64(5), resolved(teamID))
	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.addons (team_id, name, extra_api_team_rps_delete, valid_from, valid_to, added_by)
		VALUES
		($1, 'active', 2, now() - interval '1 hour', NULL, $3),
		($1, 'active with end', 3, now() - interval '1 hour', now() + interval '1 hour', $3),
		($1, 'expired', 100, now() - interval '2 hours', now() - interval '1 hour', $3),
		($1, 'future', 100, now() + interval '1 hour', NULL, $3),
		($2, 'other team', 7, now() - interval '1 hour', NULL, $3)
	`, teamID, otherTeamID, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, int64(10), resolved(teamID))
	assert.Equal(t, int64(12), resolved(otherTeamID))
	const largeRPS int64 = 1 << 32
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_delete = $1 WHERE id = 'base_v1'`, largeRPS)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.addons SET extra_api_team_rps_delete = $1 WHERE team_id = $2 AND name = 'active'`, largeRPS+1, teamID)
	require.NoError(t, err)
	assert.Equal(t, 2*largeRPS+4, resolved(teamID))
	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete
		) VALUES ($1, 1, 200, 20, 8, 8192, 10240, 7, 10240, 25600, 41, 43)
	`, teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(43), resolved(teamID))
	row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(41), row.TeamLimit.ApiTeamRpsList)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.project_limits SET api_team_rps_list = 0 WHERE team_id = $1`, teamID)
	require.NoError(t, err)
	row, err = db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Zero(t, row.TeamLimit.ApiTeamRpsList)
	assert.Equal(t, int64(43), row.TeamLimit.ApiTeamRpsDelete)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_delete = -1 WHERE id = 'base_v1'`)
	require.Error(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.addons SET extra_api_team_rps_delete = -1 WHERE team_id = $1`, teamID)
	require.Error(t, err)
}

func TestAPIDeleteRateMigrationRoundTrip(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	teamID := seedTeam(t, sqlDB, "delete-rate-migration")
	seedAddon(t, sqlDB, teamID)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_list = 17 WHERE id = 'base_v1'`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete
		) VALUES ($1, 1, 200, 20, 8, 8192, 10240, 7, 10240, 25600, 43, 0)
	`, teamID)
	require.NoError(t, err)
	store, err := database.NewStore(goose.DialectPostgres, testutils.TrackingTable)
	require.NoError(t, err)
	provider, err := goose.NewProvider("", sqlDB, dbmodule.Migrations(), goose.WithStore(store))
	require.NoError(t, err)
	otherLimits := func() string {
		t.Helper()
		var limits string
		err := sqlDB.QueryRowContext(t.Context(), `SELECT (to_jsonb(tl) - 'api_team_rps_delete' - 'max_secrets')::text FROM public.team_limits tl WHERE id = $1`, teamID).Scan(&limits)
		require.NoError(t, err)

		return limits
	}
	before := otherLimits()
	_, err = provider.DownTo(t.Context(), 20261016090000)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_delete = 13 WHERE id = 'base_v1'`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.addons SET extra_api_team_rps_delete = 4 WHERE team_id = $1`, teamID)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(17), row.TeamLimit.ApiTeamRpsDelete, "a row pushed before the column keeps the tier's rate and add-ons")
	var pushed *int64
	require.NoError(t, sqlDB.QueryRowContext(t.Context(), `SELECT api_team_rps_delete FROM public.project_limits WHERE team_id = $1`, teamID).Scan(&pushed))
	assert.Nil(t, pushed, "the migration invents no pushed rate")
	assert.JSONEq(t, before, otherLimits())
	_, err = provider.DownTo(t.Context(), 20261002234434)
	require.NoError(t, err)
	assert.JSONEq(t, before, otherLimits())
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	assert.JSONEq(t, before, otherLimits())
	row, err = db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Zero(t, row.TeamLimit.ApiTeamRpsDelete)
	assert.Equal(t, int64(43), row.TeamLimit.ApiTeamRpsList)
}

func TestProjectLimitsAcceptWritesWithoutDeleteRate(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	teamID := seedTeam(t, sqlDB, "old-limits-writer")
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.tiers SET api_team_rps_delete = 13 WHERE id = (SELECT tier FROM public.teams WHERE id = $1)`, teamID)
	require.NoError(t, err)
	oldWriter := `INSERT INTO public.project_limits (
		team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
		max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb,
		max_disk_size_mb, max_free_disk_size_mb, api_team_rps_list
	) VALUES ($1, 24, 100, 20, 8, 8192, 20480, 30, 20480, 51200, 51200, $2)
	ON CONFLICT (team_id) DO UPDATE SET api_team_rps_list = EXCLUDED.api_team_rps_list`
	_, err = sqlDB.ExecContext(t.Context(), oldWriter, teamID, 37)
	require.NoError(t, err)
	row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.EqualValues(t, 13, row.TeamLimit.ApiTeamRpsDelete, "a writer without the column leaves the tier's rate in force")
	assert.EqualValues(t, 37, row.TeamLimit.ApiTeamRpsList)

	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.project_limits SET api_team_rps_delete = 17 WHERE team_id = $1`, teamID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), oldWriter, teamID, 41)
	require.NoError(t, err)
	row, err = db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.EqualValues(t, 17, row.TeamLimit.ApiTeamRpsDelete)
	assert.EqualValues(t, 41, row.TeamLimit.ApiTeamRpsList)
}
