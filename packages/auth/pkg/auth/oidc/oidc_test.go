package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

const testIssuerURL = "https://issuer.example.com"

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	verifier, err := NewVerifier(t.Context(), Entry{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
		JWKSCacheDuration: time.Minute,
	}, server.Client())
	require.NoError(t, err)

	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
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

func TestVerifier_RejectsWrongAudience(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	verifier, err := NewVerifier(t.Context(), Entry{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
	}, server.Client())
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
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

func TestNewVerifier_DiscoveryIssuerMismatch(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, "https://different-issuer.example.com")

	_, err = NewVerifier(t.Context(), Entry{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
		},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
	}, server.Client())
	require.Error(t, err)
	require.Contains(t, err.Error(), "issuer")
}
