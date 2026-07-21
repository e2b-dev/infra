package token

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/token/jwks"
)

const (
	adminTestKeyID    = "workspace-key"
	adminTestAudience = "fx1"
)

func newAdminTestVerifier(t *testing.T) (*AdminVerifier, ed25519.PrivateKey, string) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	server := jwks.NewTestServer(t, publicKey, adminTestKeyID, jose.EdDSA, "https://unexpected.example.com")

	verifier, err := NewAdminVerifier(t.Context(), ProviderConfig{
		JWT: []jwks.Config{{
			Issuer: jwks.Issuer{
				URL:       server.URL,
				Audiences: []string{adminTestAudience},
			},
		}},
	}, server.Client())
	require.NoError(t, err)

	return verifier, privateKey, server.URL
}

func signAdminToken(t *testing.T, privateKey ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = adminTestKeyID
	signed, err := token.SignedString(privateKey)
	require.NoError(t, err)

	return signed
}

func TestAdminVerifier(t *testing.T) {
	t.Parallel()

	verifier, privateKey, issuer := newAdminTestVerifier(t)

	baseClaims := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": issuer,
			"aud": adminTestAudience,
			"exp": time.Now().Add(5 * time.Minute).Unix(),
		}
	}

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()

		_, err := verifier.Verify(t.Context(), signAdminToken(t, privateKey, baseClaims()))
		require.NoError(t, err)
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		claims := baseClaims()
		claims["exp"] = time.Now().Add(-5 * time.Minute).Unix()
		_, err := verifier.Verify(t.Context(), signAdminToken(t, privateKey, claims))
		require.Error(t, err)
	})

	t.Run("wrong audience", func(t *testing.T) {
		t.Parallel()

		claims := baseClaims()
		claims["aud"] = "other"
		_, err := verifier.Verify(t.Context(), signAdminToken(t, privateKey, claims))
		require.Error(t, err)
	})
}

func TestAdminVerifierDisabled(t *testing.T) {
	t.Parallel()

	verifier, err := NewAdminVerifier(t.Context(), ProviderConfig{}, nil)
	require.NoError(t, err)
	require.Nil(t, verifier)

	_, err = verifier.Verify(t.Context(), "any-token")
	require.ErrorContains(t, err, "not configured")
}

func TestAdminVerifierRejectsNonEdDSA(t *testing.T) {
	t.Parallel()

	verifier, _, issuer := newAdminTestVerifier(t)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": issuer,
		"aud": adminTestAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	token.Header["kid"] = adminTestKeyID
	signed, err := token.SignedString([]byte("shared-secret"))
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), signed)
	require.Error(t, err)
}

func TestAdminVerifierES256(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	server := jwks.NewTestServer(t, &privateKey.PublicKey, adminTestKeyID, jose.ES256, "https://unexpected.example.com")
	issuer := server.URL

	verifier, err := NewAdminVerifier(t.Context(), ProviderConfig{
		JWT: []jwks.Config{{
			Issuer: jwks.Issuer{
				URL:       issuer,
				Audiences: []string{adminTestAudience},
				Algorithm: jwks.SigningAlgorithmES256,
			},
		}},
	}, server.Client())
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": issuer,
		"aud": adminTestAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	token.Header["kid"] = adminTestKeyID
	signed, err := token.SignedString(privateKey)
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), signed)
	require.NoError(t, err)
}
