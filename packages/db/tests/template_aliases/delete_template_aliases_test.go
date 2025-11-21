package snapshots_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/testutils"
)

// createTestTeam creates a test team in the database using raw SQL
func createTestTeam(t *testing.T, db *client.Client) uuid.UUID {
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

// createTestTemplate creates a base env in the database (required by foreign key constraint)
func createTestTemplate(t *testing.T, db *client.Client, teamID uuid.UUID) string {
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

func createTestTemplateAlias(t *testing.T, db *client.Client, templateID string) string {
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

func TestDeleteTemplateAliases_Success(t *testing.T) {
	// Setup test database with migrations
	client := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create a test team first (required by foreign key constraint)
	teamID := createTestTeam(t, client)
	// Create a base env (required by foreign key constraint on snapshots table)
	templateID := createTestTemplate(t, client, teamID)
	_ = createTestTemplateAlias(t, client, templateID)

	// Execute UpsertSnapshot for a new snapshot
	result, err := client.DeleteOtherTemplateAliases(ctx, templateID)
	require.NoError(t, err, "Failed to create new snapshot")
	require.Len(t, result, 1, "Expected 1 deleted alias")
}

func TestDeleteTemplateAliases_NoAlias(t *testing.T) {
	// Setup test database with migrations
	client := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create a test team first (required by foreign key constraint)
	teamID := createTestTeam(t, client)
	// Create a base env (required by foreign key constraint on snapshots table)
	templateID := createTestTemplate(t, client, teamID)
	_ = createTestTemplateAlias(t, client, templateID)
	anotherTemplateID := createTestTemplate(t, client, teamID)

	// Execute UpsertSnapshot for a new snapshot
	result, err := client.DeleteOtherTemplateAliases(ctx, anotherTemplateID)
	require.NoError(t, err, "Failed to create new snapshot")
	assert.Empty(t, result, "Expected no deleted aliases")
}
