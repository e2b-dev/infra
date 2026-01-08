package testutils

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
)

// CreateTestTeam creates a test team in the database using raw SQL
func CreateTestTeam(t *testing.T, db *client.Client) uuid.UUID {
	t.Helper()
	teamID := uuid.New()

	// Insert a team directly into the database using raw SQL
	// Using the default tier 'base_v1' that is created in migrations
	err := db.TestsRawSQL(t.Context(),
		"INSERT INTO public.teams (id, name, tier, email) VALUES ($1, $2, $3, $4)",
		teamID, "Test Team "+teamID.String(), "base_v1", "test-"+teamID.String()+"@example.com",
	)
	require.NoError(t, err, "Failed to create test team")

	return teamID
}

// CreateTestTemplate creates a base env in the database (required by foreign key constraint)
func CreateTestTemplate(t *testing.T, db *client.Client, teamID uuid.UUID) string {
	t.Helper()
	envID := "base-env-" + uuid.New().String()

	// Insert a base env directly into the database
	// After the env_builds migration, envs table only has: id, team_id, public, updated_at, build_count, spawn_count, last_spawned_at
	err := db.TestsRawSQL(t.Context(),
		"INSERT INTO public.envs (id, team_id, public, updated_at) VALUES ($1, $2, $3, NOW())",
		envID, teamID, true,
	)
	require.NoError(t, err, "Failed to create test base env")

	return envID
}

func CreateTestTemplateAlias(t *testing.T, db *client.Client, templateID string) string {
	t.Helper()
	alias := "alias-" + uuid.New().String()

	// Insert a base env directly into the database
	// After the env_builds migration, envs table only has: id, team_id, public, updated_at, build_count, spawn_count, last_spawned_at
	err := db.TestsRawSQL(t.Context(),
		"INSERT INTO public.env_aliases (alias, env_id, is_renamable) VALUES ($1, $2, $3)",
		alias, templateID, true,
	)
	require.NoError(t, err, "Failed to create test base env")

	return alias
}

func CreateTestTemplateWithAlias(t *testing.T, db *client.Client, teamID uuid.UUID) (string, string) {
	t.Helper()

	templateID := CreateTestTemplate(t, db, teamID)
	alias := CreateTestTemplateAlias(t, db, templateID)

	return templateID, alias
}

// CreateTestBuild creates a build for a template with the given status
func CreateTestBuild(t *testing.T, ctx context.Context, db *client.Client, templateID string, status string) uuid.UUID {
	t.Helper()
	buildID := uuid.New()

	err := db.TestsRawSQL(ctx,
		`INSERT INTO public.env_builds 
		(id, env_id, status, vcpu, ram_mb, free_disk_size_mb, kernel_version, firecracker_version, cluster_node_id, created_at, updated_at)
		VALUES ($1, $2, $3, 2, 2048, 512, '6.1.0', '1.4.0', 'test-node', NOW(), NOW())`,
		buildID, templateID, status,
	)
	require.NoError(t, err, "Failed to create test build")

	return buildID
}

// CreateTestBuildAssignment creates a build assignment with a specific tag
func CreateTestBuildAssignment(t *testing.T, ctx context.Context, db *client.Client, templateID string, buildID uuid.UUID, tag string) {
	t.Helper()

	err := db.TestsRawSQL(ctx,
		`INSERT INTO public.env_build_assignments (env_id, build_id, tag, source, created_at)
		VALUES ($1, $2, $3, 'app', NOW())`,
		templateID, buildID, tag,
	)
	require.NoError(t, err, "Failed to create build assignment")
}

// GetBuildStatus retrieves the status of a build
func GetBuildStatus(t *testing.T, ctx context.Context, db *client.Client, buildID uuid.UUID) string {
	t.Helper()
	var status string

	err := db.TestsRawSQLQuery(ctx,
		"SELECT status FROM public.env_builds WHERE id = $1",
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&status)
			}

			return nil
		},
		buildID,
	)
	require.NoError(t, err, "Failed to get build status")

	return status
}

// DeleteTriggerBuildAssignment deletes a trigger-created build assignment
// This is useful for tests that need to create builds without the auto-assigned 'default' tag
func DeleteTriggerBuildAssignment(t *testing.T, ctx context.Context, db *client.Client, templateID string, buildID uuid.UUID, tag string) {
	t.Helper()

	err := db.TestsRawSQL(ctx,
		`DELETE FROM public.env_build_assignments 
		WHERE env_id = $1 AND build_id = $2 AND tag = $3 AND source = 'trigger'`,
		templateID, buildID, tag,
	)
	require.NoError(t, err, "Failed to delete trigger build assignment")
}
