package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/tests"
)

func TestGetJWTClaims(t *testing.T) {
	t.Parallel()
	secret1 := "testsecret1testsecret1"
	secret2 := "testsecret2testsecret2"

	ctx := t.Context()
	token1 := tests.SignTestToken(t, secret1, "1")
	token2 := tests.SignTestToken(t, secret2, "2")
	tokenEmpty := tests.SignTestToken(t, "", "3")

	t.Run("valid token for first secret", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{secret1, secret2}, token1)
		require.NoError(t, err)
		assert.Equal(t, "1", claims.Subject)
	})

	t.Run("valid token for second secret", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{secret1, secret2}, token2)
		require.NoError(t, err)
		assert.Equal(t, "2", claims.Subject)
	})

	t.Run("invalid token secret combination", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{secret1}, token2)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("no secrets", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{}, token1)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("empty secret", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{""}, tokenEmpty)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("invalid token for all secrets", func(t *testing.T) {
		t.Parallel()
		claims, err := getJWTClaims(ctx, []string{secret1, secret2}, "invalid")
		require.Error(t, err)
		assert.Nil(t, claims)
	})
}
