package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/testuilts"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func TestValidate(t *testing.T) {
	db := testuilts.SetupDatabase(t)
	ctx := context.Background()

	// Generate a valid access token
	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	require.NoError(t, err)

	userID := uuid.New()
	teamID := uuid.New()

	// Create team
	err = db.TestsRawSQL(ctx, `
		INSERT INTO "auth"."users" (id, email)
		VALUES ($1, 'test@e2b.dev')
	`, userID)
	require.NoError(t, err)

	err = db.TestsRawSQL(ctx, `
		INSERT INTO teams (id, name, email, tier)
		VALUES ($1, 'test-team', 'test@e2b.dev', 'base_v1')
	`, teamID)
	require.NoError(t, err)

	// Link user to team
	err = db.TestsRawSQL(ctx, `
		INSERT INTO users_teams (user_id, team_id, is_default)
		VALUES ($1, $2, true)
	`, userID, teamID)
	require.NoError(t, err)

	// Create access token
	_, err = db.CreateAccessToken(ctx, queries.CreateAccessTokenParams{
		ID:                    uuid.New(),
		UserID:                userID,
		AccessTokenHash:       accessToken.HashedValue,
		AccessTokenPrefix:     accessToken.Masked.Prefix,
		AccessTokenLength:     int32(accessToken.Masked.ValueLength),
		AccessTokenMaskPrefix: accessToken.Masked.MaskedValuePrefix,
		AccessTokenMaskSuffix: accessToken.Masked.MaskedValueSuffix,
		Name:                  "Test token",
	})
	require.NoError(t, err)

	// Create env
	envID := "test-env-id"
	err = db.TestsRawSQL(ctx, `
		INSERT INTO envs (id, team_id, updated_at)
		VALUES ($1, $2, NOW())
	`, envID, teamID)
	require.NoError(t, err)

	// Create env_build with "waiting" status
	buildID := uuid.New()
	err = db.TestsRawSQL(ctx, `
		INSERT INTO env_builds (id, env_id, status, dockerfile, updated_at, vcpu, ram_mb, free_disk_size_mb, firecracker_version, kernel_version, cluster_node_id)
		VALUES ($1, $2, 'waiting', 'FROM ubuntu', NOW(), 1, 1024, 1024, '0.0.0', '0.0.0', 'abc')
	`, buildID, envID)
	require.NoError(t, err)

	t.Run("valid token with waiting build", func(t *testing.T) {
		valid, err := Validate(ctx, db, accessToken.PrefixedRawValue, envID)
		require.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("valid token but no waiting build", func(t *testing.T) {
		// Update build status to "finished"
		err = db.TestsRawSQL(ctx, `
			UPDATE env_builds SET status = 'finished', finished_at = NOW() WHERE id = $1
		`, buildID)
		require.NoError(t, err)

		valid, err := Validate(ctx, db, accessToken.PrefixedRawValue, envID)
		require.NoError(t, err)
		assert.False(t, valid)

		// Restore status for other tests
		err = db.TestsRawSQL(ctx, `
			UPDATE env_builds SET status = 'waiting' WHERE id = $1
		`, buildID)
		require.NoError(t, err)
	})

	t.Run("invalid token format", func(t *testing.T) {
		valid, err := Validate(ctx, db, "invalid-token", envID)
		require.Error(t, err)
		assert.False(t, valid)
		assert.Contains(t, err.Error(), "invalid key prefix")
	})

	t.Run("valid token format but not in database", func(t *testing.T) {
		// Generate a new token that's not in the database
		newToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
		require.NoError(t, err)

		valid, err := Validate(ctx, db, newToken.PrefixedRawValue, envID)
		require.NoError(t, err)
		assert.False(t, valid)
	})

	t.Run("valid token but wrong env ID", func(t *testing.T) {
		valid, err := Validate(ctx, db, accessToken.PrefixedRawValue, "non-existent-env-id")
		require.NoError(t, err)
		assert.False(t, valid)
	})
}
