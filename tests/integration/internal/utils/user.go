package utils

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func CreateUser(t *testing.T, db *client.Client) uuid.UUID {
	t.Helper()

	userID := uuid.New()

	err := db.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, fmt.Sprintf("user-test-integration-%s@e2b.dev", userID))
	require.NoError(t, err)

	t.Cleanup(func() {
		db.TestsRawSQL(t.Context(), `
DELETE FROM auth.users WHERE id = $1
`, userID)
	})

	return userID
}

func CreateAccessToken(t *testing.T, db *client.Client, userID uuid.UUID) string {
	t.Helper()

	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	require.NoError(t, err)

	tokenWithoutPrefix := strings.TrimPrefix(accessToken.PrefixedRawValue, keys.AccessTokenPrefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	require.NoError(t, err)

	accessTokenHash := keys.NewSHA256Hashing().Hash(accessTokenBytes)

	accessTokenMask, err := keys.MaskKey(keys.AccessTokenPrefix, tokenWithoutPrefix)
	require.NoError(t, err)

	_, err = db.CreateAccessToken(t.Context(), queries.CreateAccessTokenParams{
		ID:                    uuid.New(),
		UserID:                userID,
		AccessTokenHash:       accessTokenHash,
		AccessTokenPrefix:     accessTokenMask.Prefix,
		AccessTokenLength:     int32(accessTokenMask.ValueLength),
		AccessTokenMaskPrefix: accessTokenMask.MaskedValuePrefix,
		AccessTokenMaskSuffix: accessTokenMask.MaskedValueSuffix,
		Name:                  "Integration Tests Access Token",
	})
	require.NoError(t, err)

	return accessToken.PrefixedRawValue
}
