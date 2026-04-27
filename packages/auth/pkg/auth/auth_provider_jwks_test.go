package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAuthProviderJWTVerifier_VerifyJWKS(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	jwksServer := newJWKSHTTPServer(t, &privateKey.PublicKey, keyID)

	verifier, err := NewAuthProviderJWTVerifier(AuthProviderConfig{
		JWT: AuthProviderJWTConfig{
			JWKS: &AuthProviderJWKSConfig{
				URL:           jwksServer.URL,
				CacheDuration: time.Minute,
			},
			Issuer:   "https://issuer.example.com",
			Audience: "dashboard-api",
		},
	})
	require.NoError(t, err)

	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example.com",
		"aud": "dashboard-api",
		"sub": userID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID

	signedToken, err := token.SignedString(privateKey)
	require.NoError(t, err)

	identity, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, identity.UserID)
}

func TestAuthProviderJWTVerifier_VerifyJWKSRejectsWrongAudience(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	jwksServer := newJWKSHTTPServer(t, &privateKey.PublicKey, keyID)

	verifier, err := NewAuthProviderJWTVerifier(AuthProviderConfig{
		JWT: AuthProviderJWTConfig{
			JWKS: &AuthProviderJWKSConfig{
				URL: jwksServer.URL,
			},
			Issuer:   "https://issuer.example.com",
			Audience: "dashboard-api",
		},
	})
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example.com",
		"aud": "other-audience",
		"sub": uuid.NewString(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID

	signedToken, err := token.SignedString(privateKey)
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), signedToken)
	require.Error(t, err)
}

func newJWKSHTTPServer(t *testing.T, publicKey *rsa.PublicKey, keyID string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		err := json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{
				Key:       publicKey,
				KeyID:     keyID,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			},
		}})
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	return server
}
