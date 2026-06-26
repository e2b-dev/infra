package auth

import (
	"context"
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

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

const testIssuerURL = "https://issuer.example.com"

// stubIdentityLookup implements oidc.IdentityLookup for tests by returning a
// deterministic uuid keyed on the (iss, sub) pair.
type stubIdentityLookup struct {
	mapping map[string]uuid.UUID
}

func newStubIdentityLookup() *stubIdentityLookup {
	return &stubIdentityLookup{mapping: map[string]uuid.UUID{}}
}

func (s *stubIdentityLookup) set(iss, sub string, id uuid.UUID) {
	s.mapping[iss+"\x00"+sub] = id
}

func (s *stubIdentityLookup) GetUserIdentity(_ context.Context, iss, sub string) (uuid.UUID, error) {
	if id, ok := s.mapping[iss+"\x00"+sub]; ok {
		return id, nil
	}

	return uuid.Nil, oidc.ErrIdentityNotFound
}

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

func TestNewVerifier_DisabledConfigReturnsNil(t *testing.T) {
	t.Parallel()

	verifier, err := NewVerifier(t.Context(), ProviderConfig{}, nil, nil)
	require.NoError(t, err)
	require.Nil(t, verifier)
}

func TestVerifier_VerifyJWT(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := oidc.NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	lookup := newStubIdentityLookup()
	const jwksSub = "external-subject"
	jwksUserID := uuid.New()
	lookup.set(testIssuerURL, jwksSub, jwksUserID)

	verifier, err := NewVerifier(t.Context(), ProviderConfig{
		JWT: []oidc.Config{
			{
				Issuer: oidc.Issuer{
					URL:          testIssuerURL,
					DiscoveryURL: server.URL + "/.well-known/openid-configuration",
					Audiences:    []string{"dashboard-api"},
				},
				CacheDuration: time.Minute,
			},
		},
	}, server.Client(), lookup)
	require.NoError(t, err)

	jwksToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
		"aud": "dashboard-api",
		"sub": jwksSub,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	jwksToken.Header["kid"] = keyID
	signedJWKSToken, err := jwksToken.SignedString(privateKey)
	require.NoError(t, err)

	gotJWKSUserID, _, err := verifier.Verify(t.Context(), signedJWKSToken)
	require.NoError(t, err)
	require.Equal(t, jwksUserID, gotJWKSUserID)
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

	lookup := newStubIdentityLookup()
	const tokenSub = "external-subject"
	userID := uuid.New()
	lookup.set(issuer2URL, tokenSub, userID)

	verifier, err := NewVerifier(t.Context(), ProviderConfig{
		JWT: []oidc.Config{
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
	}, httpClientForServers(server1, server2), lookup)
	require.NoError(t, err)

	// Token from issuer 2 must verify against the second strategy even when
	// the first one rejects it.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer2URL,
		"aud": "app-2",
		"sub": tokenSub,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID2

	signedToken, err := token.SignedString(privateKey2)
	require.NoError(t, err)

	gotUserID, _, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, gotUserID)
}
