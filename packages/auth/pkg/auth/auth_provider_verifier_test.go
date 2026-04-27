package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAuthProviderJWTVerifier_VerifyWithMultipleStrategies(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const (
		keyID      = "test-key"
		hmacSecret = "supabasejwtsecretsupabasejwtsecret"
	)
	jwksServer := newJWKSHTTPServer(t, &privateKey.PublicKey, keyID)

	verifier, err := NewAuthProviderJWTVerifier(t.Context(), AuthProviderConfig{
		JWT: AuthProviderJWTConfig{
			JWKS: &AuthProviderJWKSConfig{
				URL:           jwksServer.URL,
				CacheDuration: time.Minute,
			},
			HMAC: &AuthProviderHMACConfig{
				Secrets: []string{hmacSecret},
			},
			Issuer:   "https://issuer.example.com",
			Audience: "dashboard-api",
		},
	})
	require.NoError(t, err)

	hmacUserID := uuid.New()
	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "https://issuer.example.com",
		"aud": "dashboard-api",
		"sub": hmacUserID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signedHMACToken, err := hmacToken.SignedString([]byte(hmacSecret))
	require.NoError(t, err)

	hmacIdentity, err := verifier.Verify(t.Context(), signedHMACToken)
	require.NoError(t, err)
	require.Equal(t, hmacUserID, hmacIdentity.UserID)

	jwksUserID := uuid.New()
	jwksToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example.com",
		"aud": "dashboard-api",
		"sub": jwksUserID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	jwksToken.Header["kid"] = keyID
	signedJWKSToken, err := jwksToken.SignedString(privateKey)
	require.NoError(t, err)

	jwksIdentity, err := verifier.Verify(t.Context(), signedJWKSToken)
	require.NoError(t, err)
	require.Equal(t, jwksUserID, jwksIdentity.UserID)
}
