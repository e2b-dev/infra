package cfg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseAuthProviderConfig(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"jwt": {
			"issuer": "https://auth.example.com",
			"audience": "dashboard-api",
			"jwks": {
				"url": "https://auth.example.com/.well-known/jwks.json",
				"cache_duration": "30m"
			},
			"user_id_claim": "https://e2b.dev/user_id",
			"email_claim": "preferred_email"
		}
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://auth.example.com/.well-known/jwks.json", config.AuthProvider.JWT.JWKS.URL)
	require.Equal(t, "https://auth.example.com", config.AuthProvider.JWT.Issuer)
	require.Equal(t, "dashboard-api", config.AuthProvider.JWT.Audience)
	require.Equal(t, "https://e2b.dev/user_id", config.AuthProvider.JWT.UserIDClaim)
	require.Equal(t, "preferred_email", config.AuthProvider.JWT.EmailClaim)
	require.Equal(t, 30*time.Minute, config.AuthProvider.JWT.JWKS.CacheDuration)
}
