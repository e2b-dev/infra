package tests

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/auth"
)

func SignTestToken(t *testing.T, secret string, subject string) string {
	t.Helper()

	claims := auth.SupabaseClaims{
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
