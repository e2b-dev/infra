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

const secretLimitMigration = 20261016090000

func TestSecretLimitResolvesTierAndActiveAddons(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	teamID := seedTeam(t, sqlDB, "secret-limit")
	otherTeamID := seedTeam(t, sqlDB, "other-secret-limit")
	resolved := func(id uuid.UUID) int64 {
		t.Helper()
		row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), id)
		require.NoError(t, err)

		return row.TeamLimit.MaxSecrets
	}

	assert.Equal(t, int64(10), resolved(teamID), "the base tier keeps the limit the API used to hardcode")
	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.tiers (id, name, disk_mb, concurrent_instances, max_length_hours,
			default_free_disk_size_mb, max_disk_size_mb)
		VALUES ('paid_secret_limit', 'Paid', 512, 20, 1, 512, 25512)
	`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), `UPDATE public.teams SET tier = 'paid_secret_limit' WHERE id = $1`, otherTeamID)
	require.NoError(t, err)
	assert.Equal(t, int64(100), resolved(otherTeamID), "any other tier gets the paid default")

	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.addons (team_id, name, extra_max_secrets, valid_from, valid_to, added_by)
		VALUES
		($1, 'active', 2, now() - interval '1 hour', NULL, $3),
		($1, 'active with end', 3, now() - interval '1 hour', now() + interval '1 hour', $3),
		($1, 'expired', 100, now() - interval '2 hours', now() - interval '1 hour', $3),
		($1, 'future', 100, now() + interval '1 hour', NULL, $3),
		($2, 'other team', 7, now() - interval '1 hour', NULL, $3)
	`, teamID, otherTeamID, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, int64(15), resolved(teamID))
	assert.Equal(t, int64(107), resolved(otherTeamID))

	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets
		) VALUES ($1, 1, 200, 20, 8, 8192, 10240, 7, 10240, 25600, 41, 0, 40)
	`, teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(40), resolved(teamID), "a pushed limit replaces the tier and team add-on limit")
	assert.Equal(t, int64(107), resolved(otherTeamID), "another team's push does not reach this one")

	for _, statement := range []string{
		`UPDATE public.tiers SET max_secrets = 0 WHERE id = 'base_v1'`,
		`UPDATE public.addons SET extra_max_secrets = -1 WHERE team_id = $1`,
		`UPDATE public.project_limits SET max_secrets = 0 WHERE team_id = $1`,
	} {
		_, err = sqlDB.ExecContext(t.Context(), statement, teamID)
		require.Error(t, err, statement)
	}
}

func TestSecretLimitMigrationRoundTrip(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	teamID := seedTeam(t, sqlDB, "secret-limit-migration")
	seedAddon(t, sqlDB, teamID)
	_, err = sqlDB.ExecContext(t.Context(), `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets
		) VALUES ($1, 1, 200, 20, 8, 8192, 10240, 7, 10240, 25600, 43, 0, 44)
	`, teamID)
	require.NoError(t, err)
	store, err := database.NewStore(goose.DialectPostgres, testutils.TrackingTable)
	require.NoError(t, err)
	provider, err := goose.NewProvider("", sqlDB, dbmodule.Migrations(), goose.WithStore(store))
	require.NoError(t, err)
	otherLimits := func() string {
		t.Helper()
		var limits string
		err := sqlDB.QueryRowContext(t.Context(), `SELECT (to_jsonb(tl) - 'max_secrets')::text FROM public.team_limits tl WHERE id = $1`, teamID).Scan(&limits)
		require.NoError(t, err)

		return limits
	}
	before := otherLimits()
	_, err = provider.DownTo(t.Context(), secretLimitMigration-1)
	require.NoError(t, err)
	assert.JSONEq(t, before, otherLimits())
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	assert.JSONEq(t, before, otherLimits())
	row, err := db.AuthDB.GetTeamWithTierByTeamID(t.Context(), teamID)
	require.NoError(t, err)
	assert.Equal(t, int64(10), row.TeamLimit.MaxSecrets, "the pushed limit does not survive the round trip; the tier decides again")
	assert.Equal(t, int64(43), row.TeamLimit.ApiTeamRpsList)
}
