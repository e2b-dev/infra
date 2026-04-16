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

	dbClient := testutils.SetupDatabase(t)

	// Create team
	err = dbClient.AuthDb.TestsRawSQL(t.Context(), `
		INSERT INTO "auth"."users" (id, email)
		VALUES ($1, 'test@e2b.dev')
		ON CONFLICT DO NOTHING
	`, userID)
	require.NoError(t, err)

	err = dbClient.AuthDb.TestsRawSQL(t.Context(), `
		INSERT INTO teams (id, name, email, tier, slug)
		VALUES ($1, 'test-team', 'test@e2b.dev', 'base_v1', 'test-team-slug')
		ON CONFLICT DO NOTHING
	`, teamID)
	require.NoError(t, err)

	// Link user to team
	err = dbClient.AuthDb.TestsRawSQL(t.Context(), `
		INSERT INTO users_teams (user_id, team_id, is_default)
		VALUES ($1, $2, true)
		ON CONFLICT DO NOTHING
	`, userID, teamID)
	require.NoError(t, err)

	// Create access token
	_, err = dbClient.AuthDb.Write.CreateAccessToken(t.Context(), authqueries.CreateAccessTokenParams{
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

	const (
		validEnvID   = "valid-env-id"
		invalidEnvID = "invalid-env-id"
		noEnvID      = ""
	)

	testcases := []struct {
		name             string
		valid            bool
		createdEnvStatus string
		validateEnvId    string
		accessTokenUsed  string
		error            bool
	}{
		{
			name:             "valid token",
			valid:            true,
			createdEnvStatus: "waiting",
			validateEnvId:    validEnvID,
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "random access token",
			valid:            false,
			createdEnvStatus: "waiting",
			validateEnvId:    validEnvID,
			accessTokenUsed:  fmt.Sprintf("%s123abc", keys.AccessTokenPrefix),
			error:            false,
		},
		{
			name:             "wrong env ID",
			valid:            false,
			createdEnvStatus: "waiting",
			validateEnvId:    invalidEnvID,
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "no env ID",
			valid:            false,
			createdEnvStatus: "waiting",
			validateEnvId:    noEnvID,
			accessTokenUsed:  accessToken.PrefixedRawValue,
			error:            false,
		},
		{
			name:             "invalid access token",
			valid:            false,
			createdEnvStatus: "waiting",
			validateEnvId:    validEnvID,
			accessTokenUsed:  "invalid-access-token",
			error:            true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			envID := uuid.NewString()

			setupValidateTest(t, dbClient, teamID, envID, tc.createdEnvStatus)

			var validateEnvID string
			switch tc.validateEnvId {
			case validEnvID:
				validateEnvID = envID
			case invalidEnvID:
				validateEnvID = uuid.NewString()
			default:
			}

			valid, err := Validate(t.Context(), dbClient.SqlcClient, tc.accessTokenUsed, validateEnvID)
			if tc.error {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.valid, valid)
		})
	}

	t.Run("completed build status", func(t *testing.T) {
		envID := uuid.NewString()
		setupValidateTest(t, dbClient, teamID, envID, "uploaded")
		valid, err := Validate(t.Context(), dbClient.SqlcClient, accessToken.PrefixedRawValue, envID)
		assert.False(t, valid)
		assert.NoError(t, err)
	})
}

func setupValidateTest(tb testing.TB, db *testutils.Database, teamID uuid.UUID, envID, createdEnvStatus string) {
	tb.Helper()

	// Create env
	err := db.SqlcClient.TestsRawSQL(tb.Context(), `
		INSERT INTO envs (id, team_id, updated_at, source)
		VALUES ($1, $2, NOW(), 'template')
		ON CONFLICT DO NOTHING
	`, envID, teamID)
	require.NoError(tb, err)

	// Create env_build
	buildID := uuid.New()
	var finishedAt *string
	if createdEnvStatus == "uploaded" || createdEnvStatus == "success" || createdEnvStatus == "ready" {
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
