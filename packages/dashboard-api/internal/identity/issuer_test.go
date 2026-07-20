package identity

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwks"
)

func TestResolveOryIssuer_SingleJWT(t *testing.T) {
	t.Parallel()

	issuer, err := ResolveOryIssuer("https://tenant.projects.oryapis.com", []jwks.Config{
		{Issuer: jwks.Issuer{URL: "https://auth.example.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, "https://auth.example.com", issuer)
}

func TestResolveOryIssuer_MultipleJWTMatchesSDKHost(t *testing.T) {
	t.Parallel()

	issuer, err := ResolveOryIssuer("https://tenant.projects.oryapis.com", []jwks.Config{
		{Issuer: jwks.Issuer{URL: "https://auth-a.mycompany.com"}},
		{Issuer: jwks.Issuer{URL: "https://tenant.projects.oryapis.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, "https://tenant.projects.oryapis.com", issuer)
}

func TestResolveOryIssuer_MultipleJWTNoMatch(t *testing.T) {
	t.Parallel()

	_, err := ResolveOryIssuer("https://tenant.projects.oryapis.com", []jwks.Config{
		{Issuer: jwks.Issuer{URL: "https://auth-a.mycompany.com"}},
		{Issuer: jwks.Issuer{URL: "https://auth-b.mycompany.com"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no JWT issuer")
}

func TestResolveOryIssuer_NoJWTConfigs(t *testing.T) {
	t.Parallel()

	issuer, err := ResolveOryIssuer("https://tenant.projects.oryapis.com", nil)
	require.NoError(t, err)
	require.Equal(t, "https://tenant.projects.oryapis.com", issuer)
}

func TestResolveOryIssuer_NoJWTConfigsAndNoSDKURL(t *testing.T) {
	t.Parallel()

	_, err := ResolveOryIssuer("", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ORY_SDK_URL is empty")
}

func TestResolveOryIssuer_DeduplicatesSameIssuer(t *testing.T) {
	t.Parallel()

	issuer, err := ResolveOryIssuer("https://tenant.projects.oryapis.com", []jwks.Config{
		{Issuer: jwks.Issuer{URL: "https://auth.example.com"}},
		{Issuer: jwks.Issuer{URL: "https://auth.example.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, "https://auth.example.com", issuer)
}
