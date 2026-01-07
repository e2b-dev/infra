package testutils

import (
	"testing"

	"github.com/google/uuid"
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
