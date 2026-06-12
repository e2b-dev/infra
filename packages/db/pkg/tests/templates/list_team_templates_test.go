package templates

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// setupDashboardSchema creates the env_defaults table from the dashboard
// migrations (packages/db/pkg/dashboard/migrations), which testutils does not
// apply: goose tracks all migrations in one version table and the dashboard
// versions sort below the main ones, so a second "up" run would skip them.
func setupDashboardSchema(t *testing.T, ctx context.Context, db *testutils.Database) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(ctx,
		`CREATE TABLE IF NOT EXISTS public.env_defaults (
			env_id TEXT PRIMARY KEY REFERENCES public.envs(id),
			description TEXT
		)`,
	)
	require.NoError(t, err, "Failed to create env_defaults table")
}

// createReadyBuildWithAssignment creates a ready build with the given
// resources and a default-tag assignment created at the given offset from now.
func createReadyBuildWithAssignment(t *testing.T, ctx context.Context, db *testutils.Database, templateID string, vcpu, ramMb int64, assignmentAge time.Duration) uuid.UUID {
	t.Helper()
	buildID := uuid.New()

	err := db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.env_builds
		(id, env_id, status, vcpu, ram_mb, free_disk_size_mb, kernel_version, firecracker_version, created_at, updated_at)
		VALUES ($1, $2, 'uploaded', $3, $4, 512, '6.1.0', '1.4.0', NOW(), NOW())`,
		buildID, templateID, vcpu, ramMb,
	)
	require.NoError(t, err, "Failed to create ready build")

	err = db.SqlcClient.TestsRawSQL(ctx,
		`INSERT INTO public.env_build_assignments (env_id, build_id, tag, source, created_at)
		VALUES ($1, $2, 'default', 'app', NOW() - $3::interval)`,
		templateID, buildID, assignmentAge.String(),
	)
	require.NoError(t, err, "Failed to create build assignment")

	return buildID
}

// TestListTeamTemplates_AllVariantsExecute smoke-tests every sort variant with
// include_defaults enabled, so SQL errors in any of the six generated queries
// surface here instead of in production.
func TestListTeamTemplates_AllVariantsExecute(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	setupDashboardSchema(t, ctx, db)

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAlias(t, db, templateID)
	createReadyBuildWithAssignment(t, ctx, db, templateID, 2, 2048, 0)

	farFuture := time.Now().Add(100 * 365 * 24 * time.Hour)
	d := db.SqlcClient.Dashboard

	variants := map[string]func() (int, error){
		"NameAsc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByNameAsc(ctx, dashboardqueries.ListTeamTemplatesByNameAscParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
			})

			return len(rows), err
		},
		"NameDesc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByNameDesc(ctx, dashboardqueries.ListTeamTemplatesByNameDescParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
			})

			return len(rows), err
		},
		"CreatedAtAsc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByCreatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtAscParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
				CursorCreatedAt: time.Time{}, CursorID: "",
			})

			return len(rows), err
		},
		"CreatedAtDesc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByCreatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByCreatedAtDescParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
				CursorCreatedAt: farFuture, CursorID: "",
			})

			return len(rows), err
		},
		"UpdatedAtAsc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByUpdatedAtAsc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtAscParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
				CursorUpdatedAt: time.Time{}, CursorID: "",
			})

			return len(rows), err
		},
		"UpdatedAtDesc": func() (int, error) {
			rows, err := d.ListTeamTemplatesByUpdatedAtDesc(ctx, dashboardqueries.ListTeamTemplatesByUpdatedAtDescParams{
				TeamID: teamID, IncludeDefaults: true, FilterPublic: -1, LimitPlusOne: 10,
				CursorUpdatedAt: farFuture, CursorID: "",
			})

			return len(rows), err
		},
	}

	for name, run := range variants {
		count, err := run()
		require.NoError(t, err, "variant %s failed", name)
		require.Equal(t, 1, count, "variant %s returned unexpected row count", name)
	}
}

func listNameAscParams(teamID uuid.UUID, limitPlusOne int32) dashboardqueries.ListTeamTemplatesByNameAscParams {
	return dashboardqueries.ListTeamTemplatesByNameAscParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		FilterPublic:    -1,
		Search:          "",
		CursorName:      nil,
		CursorID:        nil,
		LimitPlusOne:    limitPlusOne,
	}
}

func TestListTeamTemplatesByName_SortsAndPaginates(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	setupDashboardSchema(t, ctx, db)

	teamID := testutils.CreateTestTeam(t, db)

	firstTemplate := testutils.CreateTestTemplate(t, db, teamID)
	secondTemplate := testutils.CreateTestTemplate(t, db, teamID)
	thirdTemplate := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, firstTemplate, "aaa-template", nil)
	testutils.CreateTestTemplateAliasWithName(t, db, secondTemplate, "mmm-template", nil)
	testutils.CreateTestTemplateAliasWithName(t, db, thirdTemplate, "zzz-template", nil)

	rows, err := db.SqlcClient.Dashboard.ListTeamTemplatesByNameAsc(ctx, listNameAscParams(teamID, 10))
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, []string{"aaa-template", "mmm-template", "zzz-template"},
		[]string{rows[0].NameSortKey, rows[1].NameSortKey, rows[2].NameSortKey})

	descRows, err := db.SqlcClient.Dashboard.ListTeamTemplatesByNameDesc(ctx, dashboardqueries.ListTeamTemplatesByNameDescParams{
		TeamID:          teamID,
		IncludeDefaults: false,
		FilterPublic:    -1,
		CursorName:      nil,
		CursorID:        nil,
		LimitPlusOne:    10,
	})
	require.NoError(t, err)
	require.Len(t, descRows, 3)
	require.Equal(t, "zzz-template", descRows[0].NameSortKey)

	// Keyset pagination: page of 2, then continue from the last row's cursor.
	firstPage, err := db.SqlcClient.Dashboard.ListTeamTemplatesByNameAsc(ctx, listNameAscParams(teamID, 2))
	require.NoError(t, err)
	require.Len(t, firstPage, 2)

	params := listNameAscParams(teamID, 10)
	params.CursorName = &firstPage[1].NameSortKey
	params.CursorID = &firstPage[1].TemplateID
	secondPage, err := db.SqlcClient.Dashboard.ListTeamTemplatesByNameAsc(ctx, params)
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	require.Equal(t, thirdTemplate, secondPage[0].TemplateID)
}

func TestListTeamTemplates_BuildDisplayFields(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()
	setupDashboardSchema(t, ctx, db)

	teamID := testutils.CreateTestTeam(t, db)

	buildlessTemplate := testutils.CreateTestTemplate(t, db, teamID)
	builtTemplate := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, buildlessTemplate, "aaa-buildless", nil)
	testutils.CreateTestTemplateAliasWithName(t, db, builtTemplate, "bbb-built", nil)
	buildID := createReadyBuildWithAssignment(t, ctx, db, builtTemplate, 4, 4096, 0)

	rows, err := db.SqlcClient.Dashboard.ListTeamTemplatesByNameAsc(ctx, listNameAscParams(teamID, 10))
	require.NoError(t, err)
	require.Len(t, rows, 2)

	require.Equal(t, buildlessTemplate, rows[0].TemplateID)
	require.Equal(t, uuid.Nil, rows[0].BuildID, "Template without a build must return the zero build id")
	require.Zero(t, rows[0].CpuCount)
	require.Zero(t, rows[0].MemoryMb)

	require.Equal(t, builtTemplate, rows[1].TemplateID)
	require.Equal(t, buildID, rows[1].BuildID)
	require.Equal(t, int64(4), rows[1].CpuCount)
	require.Equal(t, int64(4096), rows[1].MemoryMb)
}
