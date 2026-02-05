package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	// Generate a valid access token
	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	require.NoError(t, err)

	userID := uuid.New()
	teamID := uuid.New()
	envID := "test-env-id"

	testcases := []struct {
		name             string
		valid            bool
		createdEnvId     string
		createdEnvStatus string
		validateEnvId    string
		accessTokenUsed  string
		error            bool
	}{
		{
			name:             "valid token",
			valid:            true,
			createdEnvId:     envID,
			createdEnvStatus: "waiting",
			validateEnvId:    envID,
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "random access token",
			valid:            false,
			createdEnvId:     envID,
			createdEnvStatus: "waiting",
			validateEnvId:    envID,
			accessTokenUsed:  fmt.Sprintf("%s123abc", keys.AccessTokenPrefix),
			error:            false,
		},
		{
			name:             "wrong env ID",
			valid:            false,
			createdEnvId:     envID,
			createdEnvStatus: "waiting",
			validateEnvId:    "non-existent-env-id",
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "no env ID",
			valid:            false,
			createdEnvId:     envID,
			createdEnvStatus: "waiting",
			validateEnvId:    "",
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "invalid status",
			valid:            false,
			createdEnvId:     envID,
			createdEnvStatus: "finished",
			validateEnvId:    envID,
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "invalid access token",
			valid:            false,
			createdEnvId:     envID,
			createdEnvStatus: "waiting",
			validateEnvId:    envID,
			accessTokenUsed:  "invalid-access-token",
			error:            true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dbClient := testutils.SetupDatabase(t)
			setupValidateTest(t, dbClient, userID, teamID, accessToken, tc.createdEnvId, tc.createdEnvStatus)

			valid, err := Validate(t.Context(), dbClient.SqlcClient, tc.accessTokenUsed, tc.validateEnvId)
			if tc.error {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.valid, valid)
		})
	}
}

func setupValidateTest(tb testing.TB, db *testutils.Database, userID, teamID uuid.UUID, accessToken keys.Key, envID, createdEnvStatus string) {
	tb.Helper()

	// Create team
	err := db.AuthDb.TestsRawSQL(tb.Context(), `
		INSERT INTO "auth"."users" (id, email)
		VALUES ($1, 'test@e2b.dev')
	`, userID)
	require.NoError(tb, err)

	err = db.AuthDb.TestsRawSQL(tb.Context(), `
		INSERT INTO teams (id, name, email, tier, slug)
		VALUES ($1, 'test-team', 'test@e2b.dev', 'base_v1', 'test-team-slug')
	`, teamID)
	require.NoError(tb, err)

	// Link user to team
	err = db.AuthDb.TestsRawSQL(tb.Context(), `
		INSERT INTO users_teams (user_id, team_id, is_default)
		VALUES ($1, $2, true)
	`, userID, teamID)
	require.NoError(tb, err)

	// Create access token
	_, err = db.AuthDb.Write.CreateAccessToken(tb.Context(), authqueries.CreateAccessTokenParams{
		ID:                    uuid.New(),
		UserID:                userID,
		AccessTokenHash:       accessToken.HashedValue,
		AccessTokenPrefix:     accessToken.Masked.Prefix,
		AccessTokenLength:     int32(accessToken.Masked.ValueLength),
		AccessTokenMaskPrefix: accessToken.Masked.MaskedValuePrefix,
		AccessTokenMaskSuffix: accessToken.Masked.MaskedValueSuffix,
		Name:                  "Test token",
	})
	require.NoError(tb, err)

	// Create env
	err = db.SqlcClient.TestsRawSQL(tb.Context(), `
		INSERT INTO envs (id, team_id, updated_at)
		VALUES ($1, $2, NOW())
	`, envID, teamID)
	require.NoError(tb, err)

	// Create env_build
	buildID := uuid.New()
	var finishedAt *string
	if createdEnvStatus == "finished" {
		now := time.Now().Format(time.RFC3339)
		finishedAt = &now
	}
	err = db.SqlcClient.TestsRawSQL(tb.Context(), `
		INSERT INTO env_builds (id, status, finished_at, dockerfile, updated_at, vcpu, ram_mb, free_disk_size_mb, firecracker_version, kernel_version, cluster_node_id)
		VALUES ($1, $2, $3, 'FROM ubuntu', NOW(), 1, 1024, 1024, '0.0.0', '0.0.0', 'abc')
	`, buildID, createdEnvStatus, finishedAt)
	require.NoError(tb, err)

	err = db.SqlcClient.TestsRawSQL(tb.Context(), `
		INSERT INTO env_build_assignments (env_id, build_id, tag)
		VALUES ($1, $2, 'default')
	`, envID, buildID)
	require.NoError(tb, err)
}
