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

	var columnCount int64
	var nullableWithoutDefaults bool
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*), BOOL_AND(is_nullable = 'YES' AND column_default IS NULL)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (table_name, column_name) IN (
			('tiers', 'default_free_disk_size_mb'),
			('tiers', 'max_disk_size_mb'),
			('addons', 'extra_max_disk_size_mb')
		  )
	`).Scan(&columnCount, &nullableWithoutDefaults)
	require.NoError(t, err)
	require.Equal(t, int64(3), columnCount)
	require.True(t, nullableWithoutDefaults)

	var v1Columns, v2Columns string
	err = sqlDB.QueryRowContext(ctx, `
		SELECT string_agg(column_name, ',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'team_limits'
	`).Scan(&v1Columns)
	require.NoError(t, err)
	err = sqlDB.QueryRowContext(ctx, `
		SELECT string_agg(column_name, ',' ORDER BY ordinal_position)
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'team_limits_v2'
	`).Scan(&v2Columns)
	require.NoError(t, err)

	const legacyColumns = "id,max_length_hours,concurrent_sandboxes,concurrent_template_builds," +
		"max_vcpu,max_ram_mb,disk_mb,events_ttl_days"
	require.Equal(t, legacyColumns, v1Columns)
	require.Equal(t, legacyColumns+",default_free_disk_size_mb,max_disk_size_mb", v2Columns)

	var securityInvoker bool
	err = sqlDB.QueryRowContext(ctx, `
		SELECT COALESCE(reloptions @> ARRAY['security_invoker=on']::text[], false)
		FROM pg_class
		WHERE oid = 'public.team_limits_v2'::regclass
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

	var legacyDisk int64
	err = sqlDB.QueryRowContext(ctx, `
		SELECT disk_mb FROM public.team_limits WHERE id = $1
	`, teamID).Scan(&legacyDisk)
	require.NoError(t, err)
	require.Equal(t, int64(11240), legacyDisk)

	var v2Disk, defaultFree, maximum int64
	err = sqlDB.QueryRowContext(ctx, `
		SELECT disk_mb, default_free_disk_size_mb, max_disk_size_mb
		FROM public.team_limits_v2
		WHERE id = $1
	`, teamID).Scan(&v2Disk, &defaultFree, &maximum)
	require.NoError(t, err)
	require.Equal(t, int64(11240), v2Disk)
	require.Equal(t, int64(9000), defaultFree)
	require.Equal(t, int64(33000), maximum)
}
