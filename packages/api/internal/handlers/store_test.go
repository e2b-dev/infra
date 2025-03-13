package handlers

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func signToken(t *testing.T, secret string, subject string) string {
	claims := supabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), // 1 hour expiry
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
	secret1 := "testsecret1testsecret1"
	secret2 := "testsecret2testsecret2"

	token1 := signToken(t, secret1, "1")
	token2 := signToken(t, secret2, "2")
	tokenEmpty := signToken(t, "", "3")

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
