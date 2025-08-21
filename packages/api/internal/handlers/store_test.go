package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/tests"
)

func TestGetJWTClaims(t *testing.T) {
	secret1 := "testsecret1testsecret1"
	secret2 := "testsecret2testsecret2"

	token1 := tests.SignTestToken(t, secret1, "1")
	token2 := tests.SignTestToken(t, secret2, "2")
	tokenEmpty := tests.SignTestToken(t, "", "3")

	t.Run("valid token for first secret", func(t *testing.T) {
		claims, err := getJWTClaims([]string{secret1, secret2}, token1)
		assert.NoError(t, err)
		assert.Equal(t, "1", claims.Subject)
	})

	t.Run("valid token for second secret", func(t *testing.T) {
		claims, err := getJWTClaims([]string{secret1, secret2}, token2)
		assert.NoError(t, err)
		assert.Equal(t, "2", claims.Subject)
	})

	t.Run("invalid token secret combination", func(t *testing.T) {
		claims, err := getJWTClaims([]string{secret1}, token2)
		assert.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("no secrets", func(t *testing.T) {
		claims, err := getJWTClaims([]string{}, token1)
		assert.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("empty secret", func(t *testing.T) {
		claims, err := getJWTClaims([]string{""}, tokenEmpty)
		assert.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("invalid token for all secrets", func(t *testing.T) {
		claims, err := getJWTClaims([]string{secret1, secret2}, "invalid")
		assert.Error(t, err)
		assert.Nil(t, claims)
	})
}
