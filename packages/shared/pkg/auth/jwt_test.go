package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signTestToken(t *testing.T, secret string, subject string) string {
	t.Helper()

	claims := SupabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "test",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signedToken, err := token.SignedString([]byte(secret))
	assert.NoError(t, err)

	return signedToken
}

func TestGetJWTClaims(t *testing.T) {
	t.Parallel()
	secret1 := "testsecret1testsecret1"
	secret2 := "testsecret2testsecret2"

	ctx := t.Context()
	token1 := signTestToken(t, secret1, "1")
	token2 := signTestToken(t, secret2, "2")
	tokenEmpty := signTestToken(t, "", "3")

	t.Run("valid token for first secret", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{secret1, secret2}, token1)
		require.NoError(t, err)
		assert.Equal(t, "1", claims.Subject)
	})

	t.Run("valid token for second secret", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{secret1, secret2}, token2)
		require.NoError(t, err)
		assert.Equal(t, "2", claims.Subject)
	})

	t.Run("invalid token secret combination", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{secret1}, token2)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("no secrets", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{}, token1)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("empty secret", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{""}, tokenEmpty)
		require.Error(t, err)
		assert.Nil(t, claims)
	})

	t.Run("invalid token for all secrets", func(t *testing.T) {
		t.Parallel()
		claims, err := GetJWTClaims(ctx, []string{secret1, secret2}, "invalid")
		require.Error(t, err)
		assert.Nil(t, claims)
	})
}
