package cfg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

func TestParseAuthProviderConfig(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"jwt": [
			{
				"issuer": {
					"url": "https://auth.example.com",
					"audiences": ["dashboard-api", "other"],
					"audienceMatchPolicy": "MatchAny"
				},
				"claimMappings": {
					"username": { "claim": "https://e2b.dev/user_id" }
				},
				"jwksCacheDuration": "30m"
			}
		]
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.Len(t, config.AuthProvider.JWT, 1)

	entry := config.AuthProvider.JWT[0]
	require.Equal(t, "https://auth.example.com", entry.Issuer.URL)
	require.Equal(t, []string{"dashboard-api", "other"}, entry.Issuer.Audiences)
	require.Equal(t, jwtutil.AudienceMatchAny, entry.Issuer.AudienceMatchPolicy)
	require.Equal(t, "https://e2b.dev/user_id", entry.ClaimMappings.Username.Claim)
	require.Equal(t, 30*time.Minute, entry.JWKSCacheDuration)
}

func TestParseAuthProviderConfigBearer(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"bearer": [
			{
				"hmac": { "secrets": ["secret-1", "secret-2"] },
				"claimMappings": { "username": { "claim": "sub" } }
			}
		]
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.Len(t, config.AuthProvider.Bearer, 1)
	require.Equal(t, []string{"secret-1", "secret-2"}, config.AuthProvider.Bearer[0].HMAC.Secrets)
	require.Equal(t, "sub", config.AuthProvider.Bearer[0].ClaimMappings.Username.Claim)
}
