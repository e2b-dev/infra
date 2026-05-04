package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/bearer"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

const testIssuerURL = "https://issuer.example.com"

// httpClientForServers returns an HTTP client whose root CA pool trusts every
// supplied test server. Required when a single verifier needs to talk to
// multiple httptest TLS servers (each uses its own self-signed cert).
func httpClientForServers(servers ...*httptest.Server) *http.Client {
	pool := x509.NewCertPool()
	for _, s := range servers {
		pool.AddCert(s.Certificate())
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}

func TestVerifier_VerifyWithMultipleStrategies(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const (
		keyID      = "test-key"
		hmacSecret = "supabasejwtsecretsupabasejwtsecret"
	)
	server := oidc.NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	verifier, err := newVerifier(t.Context(), ProviderConfig{
		JWT: []oidc.Entry{
			{
				Issuer: oidc.Issuer{
					URL:          testIssuerURL,
					DiscoveryURL: server.URL + "/.well-known/openid-configuration",
					Audiences:    []string{"dashboard-api"},
				},
				JWKSCacheDuration: time.Minute,
			},
		},
		Bearer: []bearer.Entry{
			{
				HMAC: &bearer.HMACConfig{Secrets: []string{hmacSecret}},
			},
		},
	}, server.Client())
	require.NoError(t, err)

	hmacUserID := uuid.New()
	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
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
		"iss": testIssuerURL,
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

func TestVerifier_VerifyMultipleJWTIssuers(t *testing.T) {
	t.Parallel()

	privateKey1, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateKey2, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const (
		keyID1     = "key-1"
		keyID2     = "key-2"
		issuer1URL = "https://issuer-one.example.com"
		issuer2URL = "https://issuer-two.example.com"
	)

	server1 := oidc.NewTestServer(t, &privateKey1.PublicKey, keyID1, issuer1URL)
	server2 := oidc.NewTestServer(t, &privateKey2.PublicKey, keyID2, issuer2URL)

	verifier, err := newVerifier(t.Context(), ProviderConfig{
		JWT: []oidc.Entry{
			{
				Issuer: oidc.Issuer{
					URL:          issuer1URL,
					DiscoveryURL: server1.URL + "/.well-known/openid-configuration",
					Audiences:    []string{"app-1"},
				},
			},
			{
				Issuer: oidc.Issuer{
					URL:          issuer2URL,
					DiscoveryURL: server2.URL + "/.well-known/openid-configuration",
					Audiences:    []string{"app-2"},
				},
			},
		},
	}, httpClientForServers(server1, server2))
	require.NoError(t, err)

	// Token from issuer 2 must verify against the second strategy even when
	// the first one rejects it.
	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer2URL,
		"aud": "app-2",
		"sub": userID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID2

	signedToken, err := token.SignedString(privateKey2)
	require.NoError(t, err)

	identity, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, identity.UserID)
}
