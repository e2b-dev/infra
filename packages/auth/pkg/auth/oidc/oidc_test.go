package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testIssuerURL = "https://issuer.example.com"

// stubIdentityLookup is a test double for IdentityLookup.
type stubIdentityLookup struct {
	userID uuid.UUID
	err    error
	// capture of last call args for assertions.
	lastIss string
	lastSub string
}

func (s *stubIdentityLookup) GetUserIdentity(_ context.Context, iss, sub string) (uuid.UUID, error) {
	s.lastIss = iss
	s.lastSub = sub
	if s.err != nil {
		return uuid.Nil, s.err
	}

	return s.userID, nil
}

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	internalUserID := uuid.New()
	lookup := &stubIdentityLookup{userID: internalUserID}

	verifier, err := NewVerifier(t.Context(), Config{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
		CacheDuration: time.Minute,
	}, server.Client(), lookup)
	require.NoError(t, err)

	const tokenSub = "external-subject-123"
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
		"aud": "dashboard-api",
		"sub": tokenSub,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID

	signedToken, err := token.SignedString(privateKey)
	require.NoError(t, err)

	gotUserID, _, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, internalUserID, gotUserID)
	require.Equal(t, testIssuerURL, lookup.lastIss)
	require.Equal(t, tokenSub, lookup.lastSub)
}

func TestVerifier_IdentityNotFound(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	lookup := &stubIdentityLookup{err: ErrIdentityNotFound}

	verifier, err := NewVerifier(t.Context(), Config{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
	}, server.Client(), lookup)
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
		"aud": "dashboard-api",
		"sub": "unknown-subject",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID

	signedToken, err := token.SignedString(privateKey)
	require.NoError(t, err)

	gotUserID, _, err := verifier.Verify(t.Context(), signedToken)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrIdentityNotFound)
	require.Equal(t, uuid.Nil, gotUserID)
}

func TestVerifier_IdentityLookupError(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	lookupErr := errors.New("boom")
	lookup := &stubIdentityLookup{err: lookupErr}

	verifier, err := NewVerifier(t.Context(), Config{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
	}, server.Client(), lookup)
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuerURL,
		"aud": "dashboard-api",
		"sub": "external-subject",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID

	signedToken, err := token.SignedString(privateKey)
	require.NoError(t, err)

	gotUserID, _, err := verifier.Verify(t.Context(), signedToken)
	require.Error(t, err)
	require.ErrorIs(t, err, lookupErr)
	require.NotErrorIs(t, err, ErrIdentityNotFound)
	require.Equal(t, uuid.Nil, gotUserID)
}

func TestVerifier_RejectsWrongAudience(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, testIssuerURL)

	lookup := &stubIdentityLookup{userID: uuid.New()}
	verifier, err := NewVerifier(t.Context(), Config{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
			Audiences:    []string{"dashboard-api"},
		},
	}, server.Client(), lookup)
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

	_, _, err = verifier.Verify(t.Context(), signedToken)
	require.Error(t, err)
	// audience rejection must happen before the identity lookup is consulted.
	require.Empty(t, lookup.lastIss)
	require.Empty(t, lookup.lastSub)
}

func TestNewVerifier_DiscoveryIssuerMismatch(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	const keyID = "test-key"
	server := NewTestServer(t, &privateKey.PublicKey, keyID, "https://different-issuer.example.com")

	_, err = NewVerifier(t.Context(), Config{
		Issuer: Issuer{
			URL:          testIssuerURL,
			DiscoveryURL: server.URL + "/.well-known/openid-configuration",
		},
	}, server.Client(), &stubIdentityLookup{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "issuer")
}
