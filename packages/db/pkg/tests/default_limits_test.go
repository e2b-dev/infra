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
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

const defaultLimitsMigration = 20261019080937

// A team without project_limits takes its limits from this row: a second row
// would duplicate every such team, and no row would leave them unable to
// authenticate.
func TestDefaultLimitsKeepsExactlyOneRow(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	rows := func() int {
		t.Helper()
		var count int
		require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM public.default_limits`).Scan(&count))

		return count
	}
	require.Equal(t, 1, rows())

	for _, statement := range []string{
		`INSERT INTO public.default_limits SELECT * FROM public.default_limits`,
		`INSERT INTO public.default_limits (id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets)
			VALUES (false, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)`,
		`UPDATE public.default_limits SET id = false`,
		`DELETE FROM public.default_limits`,
		`TRUNCATE public.default_limits`,
		`UPDATE public.default_limits SET concurrent_sandboxes = 0`,
		`UPDATE public.default_limits SET max_secrets = 0`,
		`UPDATE public.default_limits SET api_team_rps_delete = -1`,
		`UPDATE public.default_limits SET default_free_disk_size_mb = max_disk_size_mb + 1`,
	} {
		_, err = sqlDB.ExecContext(ctx, statement)
		require.Error(t, err, statement)
	}

	_, err = sqlDB.ExecContext(ctx, `UPDATE public.default_limits SET concurrent_sandboxes = 64`)
	require.NoError(t, err)
	require.Equal(t, 1, rows())
}

// Tiers and add-ons are being retired as a source of limits, so the migration
// pins every team without a row to what team_limits resolved for it, and
// starts the defaults from the deployment's own base tier.
func TestDefaultLimitsMigrationKeepsWhatEveryTeamResolvedTo(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	provider := migrationProvider(t, sqlDB)

	_, err = provider.DownTo(ctx, defaultLimitsMigration-1)
	require.NoError(t, err)

	_, err = sqlDB.ExecContext(ctx, `
		UPDATE public.tiers
		SET concurrent_instances = 33, max_vcpu = 3, api_team_rps_list = 7, api_team_rps_delete = 9, max_secrets = 4
		WHERE id = 'base_v1'
	`)
	require.NoError(t, err)

	onBase := seedTeam(t, sqlDB, "pinned-base")
	onOtherTierWithAddons := seedTeam(t, sqlDB, "pinned-other-tier-addons")
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.tiers (id, name, disk_mb, concurrent_instances, max_length_hours, max_vcpu, max_ram_mb,
			concurrent_template_builds, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets)
		VALUES ('pinned_other', 'Other', 20480, 100, 24, 16, 16384, 21, 30, 20480, 51200, 20, 100, 100)
	`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `UPDATE public.teams SET tier = 'pinned_other' WHERE id = $1`, onOtherTierWithAddons)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.addons (
			team_id, name, extra_concurrent_sandboxes, extra_concurrent_template_builds,
			extra_max_vcpu, extra_max_ram_mb, extra_disk_mb, extra_events_ttl_days,
			extra_max_disk_size_mb, extra_api_team_rps_list, extra_api_team_rps_delete, extra_max_secrets,
			valid_from, valid_to, added_by
		) VALUES
		($1, 'active', 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, now() - interval '1 hour', NULL, $2),
		($1, 'expired', 100, 100, 100, 100, 100, 100, 100, 100, 100, 100,
		 now() - interval '2 hours', now() - interval '1 hour', $2)
	`, onOtherTierWithAddons, uuid.Nil)
	require.NoError(t, err)
	pushed := seedTeam(t, sqlDB, "pinned-pushed")
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.project_limits (
			team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
			max_vcpu, max_ram_mb, disk_mb, events_ttl_days, default_free_disk_size_mb, max_disk_size_mb,
			api_team_rps_list, api_team_rps_delete, max_secrets, updated_at
		) VALUES ($1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, '2026-01-01T00:00:00Z')
	`, pushed)
	require.NoError(t, err)
	teams := []uuid.UUID{onBase, onOtherTierWithAddons, pushed}

	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, sqlDB.QueryRowContext(ctx, query, args...).Scan(&n))

		return n
	}
	pushedRow := func() string {
		t.Helper()
		var row string
		require.NoError(t, sqlDB.QueryRowContext(ctx,
			`SELECT to_jsonb(pl)::text FROM public.project_limits pl WHERE team_id = $1`, pushed).Scan(&row))

		return row
	}
	const unpinned = `SELECT count(*) FROM public.teams t
		WHERE NOT EXISTS (SELECT 1 FROM public.project_limits pl WHERE pl.team_id = t.id)`
	const stored = `SELECT count(*) FROM public.project_limits`

	before := viewLimits(t, sqlDB, teams)
	require.Len(t, before, len(teams))
	assert.EqualValues(t, 100+5, before[onOtherTierWithAddons].ConcurrentSandboxes, "the view sums the tier and active add-ons")
	pushedBefore := pushedRow()
	missing := count(unpinned)
	storedBefore := count(stored)
	assert.Equal(t, 2, missing, "the two teams seeded without a row")

	_, err = provider.UpTo(ctx, defaultLimitsMigration)
	require.NoError(t, err)

	assert.Equal(t, before, pinnedLimits(t, sqlDB, teams), "every team is pinned to what it resolved to")
	assert.Equal(t, before, viewLimits(t, sqlDB, teams), "no team's limits move")
	assert.Zero(t, count(unpinned))
	assert.Equal(t, storedBefore+missing, count(stored))
	assert.JSONEq(t, pushedBefore, pushedRow(), "a pushed row is left alone")
	assert.Zero(t, count(`SELECT count(*) FROM projection.project_limits`), "the ledger is not advanced")
	assert.Zero(t, count(`SELECT count(*) FROM public.project_limits
		WHERE max_free_disk_size_mb IS DISTINCT FROM max_disk_size_mb AND team_id <> $1`, pushed))

	var defaults string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT (to_jsonb(d) - 'id')::text FROM public.default_limits d`).Scan(&defaults))
	assert.JSONEq(t, `{
		"max_length_hours": 1, "concurrent_sandboxes": 33, "concurrent_template_builds": 20,
		"max_vcpu": 3, "max_ram_mb": 8192, "disk_mb": 512, "events_ttl_days": 7,
		"default_free_disk_size_mb": 512, "max_disk_size_mb": 25600,
		"api_team_rps_list": 7, "api_team_rps_delete": 9, "max_secrets": 4
	}`, defaults, "seeded from the tuned base tier")

	_, err = sqlDB.ExecContext(ctx, `UPDATE public.default_limits SET concurrent_sandboxes = 77`)
	require.NoError(t, err)
	assert.Equal(t, before, pinnedLimits(t, sqlDB, teams), "pinned teams do not follow the defaults")
	seedTeam(t, sqlDB, "after-defaults")

	_, err = provider.DownTo(ctx, defaultLimitsMigration-1)
	require.NoError(t, err)
	var table *string
	require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT to_regclass('public.default_limits')::text`).Scan(&table))
	assert.Nil(t, table)
	assert.Equal(t, 1, count(unpinned), "down keeps the rows it pinned")
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	assert.Zero(t, count(unpinned))
}

// Clamping would change the team's limits silently; failing leaves the
// deployment on the previous release until someone looks.
func TestDefaultLimitsMigrationFailsOnATeamItCannotPin(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	provider := migrationProvider(t, sqlDB)

	_, err = provider.DownTo(ctx, defaultLimitsMigration-1)
	require.NoError(t, err)
	teamID := seedTeam(t, sqlDB, "unpinnable")
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.addons (team_id, name, extra_disk_mb, extra_max_disk_size_mb, added_by)
		VALUES ($1, 'free disk above the ceiling', 30000, 0, $2)
	`, teamID, uuid.Nil)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, defaultLimitsMigration)
	require.Error(t, err)
	assertNoDefaultLimitsTable(t, sqlDB)
}

func TestDefaultLimitsMigrationFailsWithoutTheBaseTier(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	provider := migrationProvider(t, sqlDB)

	_, err = provider.DownTo(ctx, defaultLimitsMigration-1)
	require.NoError(t, err)
	// Every database has a system team, on base_v1.
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.tiers (id, name, disk_mb, concurrent_instances, max_length_hours,
			default_free_disk_size_mb, max_disk_size_mb)
		VALUES ('not_base', 'Not base', 512, 20, 1, 512, 25600)
	`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `UPDATE public.teams SET tier = 'not_base'`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `DELETE FROM public.tiers WHERE id = 'base_v1'`)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, defaultLimitsMigration)
	require.ErrorContains(t, err, "tier base_v1 is missing")
	assertNoDefaultLimitsTable(t, sqlDB)
}

func assertNoDefaultLimitsTable(t *testing.T, sqlDB *sql.DB) {
	t.Helper()

	var table *string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(), `SELECT to_regclass('public.default_limits')::text`).Scan(&table))
	assert.Nil(t, table, "the migration rolls back whole")
}

func viewLimits(t *testing.T, sqlDB *sql.DB, teamIDs []uuid.UUID) map[uuid.UUID]authqueries.TeamLimit {
	t.Helper()

	return scanLimits(t, sqlDB, `
		SELECT id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
		       max_vcpu, max_ram_mb, disk_mb, events_ttl_days,
		       default_free_disk_size_mb, max_disk_size_mb, max_free_disk_size_mb,
		       api_team_rps_list, api_team_rps_delete, max_secrets
		FROM public.team_limits
		WHERE id::text = ANY($1)`, teamIDs)
}

func scanLimits(t *testing.T, sqlDB *sql.DB, query string, teamIDs []uuid.UUID) map[uuid.UUID]authqueries.TeamLimit {
	t.Helper()

	ids := make([]string, 0, len(teamIDs))
	for _, id := range teamIDs {
		ids = append(ids, id.String())
	}
	rows, err := sqlDB.QueryContext(t.Context(), query, ids)
	require.NoError(t, err)
	defer rows.Close()

	limits := map[uuid.UUID]authqueries.TeamLimit{}
	for rows.Next() {
		var row authqueries.TeamLimit
		require.NoError(t, rows.Scan(&row.ID, &row.MaxLengthHours, &row.ConcurrentSandboxes,
			&row.ConcurrentTemplateBuilds, &row.MaxVcpu, &row.MaxRamMb, &row.DiskMb, &row.EventsTtlDays,
			&row.DefaultFreeDiskSizeMb, &row.MaxDiskSizeMb, &row.MaxFreeDiskSizeMb,
			&row.ApiTeamRpsList, &row.ApiTeamRpsDelete, &row.MaxSecrets))
		limits[row.ID] = row
	}
	require.NoError(t, rows.Err())

	return limits
}

func pinnedLimits(t *testing.T, sqlDB *sql.DB, teamIDs []uuid.UUID) map[uuid.UUID]authqueries.TeamLimit {
	t.Helper()

	return scanLimits(t, sqlDB, `
		SELECT team_id, max_length_hours, concurrent_sandboxes, concurrent_template_builds,
		       max_vcpu, max_ram_mb, disk_mb, events_ttl_days,
		       default_free_disk_size_mb, max_disk_size_mb, max_disk_size_mb,
		       api_team_rps_list, api_team_rps_delete, max_secrets
		FROM public.project_limits
		WHERE team_id::text = ANY($1)`, teamIDs)
}

func migrationProvider(t *testing.T, sqlDB *sql.DB) *goose.Provider {
	t.Helper()

	store, err := database.NewStore(goose.DialectPostgres, testutils.TrackingTable)
	require.NoError(t, err)
	provider, err := goose.NewProvider("", sqlDB, dbmodule.Migrations(), goose.WithStore(store))
	require.NoError(t, err)

	return provider
}
