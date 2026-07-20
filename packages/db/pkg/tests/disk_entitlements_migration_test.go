package tests

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	testqueries "github.com/e2b-dev/infra/packages/db/pkg/testutils/queries"
)

func TestDiskEntitlementsMigration(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	sqlDB, err := sql.Open("pgx", db.ConnStr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	var tierColumnCount int64
	var tierColumnsNotNullWithoutDefaults bool
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*), BOOL_AND(data_type = 'bigint' AND is_nullable = 'NO' AND column_default IS NULL)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (table_name, column_name) IN (
			('tiers', 'default_free_disk_size_mb'),
			('tiers', 'max_disk_size_mb')
		  )
	`).Scan(&tierColumnCount, &tierColumnsNotNullWithoutDefaults)
	require.NoError(t, err)
	require.Equal(t, int64(2), tierColumnCount)
	require.True(t, tierColumnsNotNullWithoutDefaults, "tiers entitlement columns must be NOT NULL now that every row is populated")

	// extra_max_disk_size_mb stays nullable so legacy add-on writers can keep omitting it during rollout.
	var addonColumnCount int64
	var addonColumnNullableWithoutDefault bool
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*), BOOL_AND(data_type = 'bigint' AND is_nullable = 'YES' AND column_default IS NULL)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'addons' AND column_name = 'extra_max_disk_size_mb'
	`).Scan(&addonColumnCount, &addonColumnNullableWithoutDefault)
	require.NoError(t, err)
	require.Equal(t, int64(1), addonColumnCount)
	require.True(t, addonColumnNullableWithoutDefault)

	var tierCount, invalidTierCount int64
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (
			WHERE default_free_disk_size_mb IS DISTINCT FROM disk_mb
			   OR max_disk_size_mb IS DISTINCT FROM disk_mb + 25000
		)
		FROM public.tiers
	`).Scan(&tierCount, &invalidTierCount)
	require.NoError(t, err)
	require.NotZero(t, tierCount)
	require.Zero(t, invalidTierCount)

	var viewColumns string
	err = sqlDB.QueryRowContext(ctx, `
		SELECT string_agg(column_name, ',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'team_limits'
	`).Scan(&viewColumns)
	require.NoError(t, err)

	const legacyColumns = "id,max_length_hours,concurrent_sandboxes,concurrent_template_builds," +
		"max_vcpu,max_ram_mb,disk_mb,events_ttl_days"
	require.Equal(t, legacyColumns+",default_free_disk_size_mb,max_disk_size_mb", viewColumns)

	var securityInvoker bool
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COALESCE(reloptions @> ARRAY['security_invoker=on']::text[], false)
		FROM pg_class
		WHERE oid = 'public.team_limits'::regclass
	`).Scan(&securityInvoker)
	require.NoError(t, err)
	require.True(t, securityInvoker)

	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.tiers (
			id, name, disk_mb, concurrent_instances, max_length_hours,
			default_free_disk_size_mb, max_disk_size_mb
		)
		VALUES ('en-1038-test', 'EN-1038 test', 10240, 1, 24, 8000, 30000)
	`)
	require.NoError(t, err)

	teamID := uuid.New()
	err = db.TestQueries.InsertTestTeam(ctx, testqueries.InsertTestTeamParams{
		ID:    teamID,
		Name:  "EN-1038 migration test",
		Tier:  "en-1038-test",
		Email: "en-1038-migration@example.com",
		Slug:  "en-1038-migration",
	})
	require.NoError(t, err)

	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO public.addons (
			team_id, name, extra_disk_mb, extra_max_disk_size_mb, added_by
		)
		VALUES ($1, 'EN-1038 test add-on', 1000, 3000,
			'00000000-0000-0000-0000-000000000000')
	`, teamID)
	require.NoError(t, err)

	var disk, defaultFree, maximum int64
	err = sqlDB.QueryRowContext(ctx, `
		SELECT disk_mb, default_free_disk_size_mb, max_disk_size_mb
		FROM public.team_limits
		WHERE id = $1
	`, teamID).Scan(&disk, &defaultFree, &maximum)
	require.NoError(t, err)
	require.Equal(t, int64(11240), disk)
	require.Equal(t, int64(9000), defaultFree)
	require.Equal(t, int64(33000), maximum)
}
