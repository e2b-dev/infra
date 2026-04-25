package cfg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseOAuthConfig(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("OAUTH_JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
	t.Setenv("OAUTH_ISSUER", "https://auth.example.com")
	t.Setenv("OAUTH_AUDIENCE", "dashboard-api")
	t.Setenv("OAUTH_USER_ID_CLAIM", "https://e2b.dev/user_id")
	t.Setenv("OAUTH_EMAIL_CLAIM", "preferred_email")
	t.Setenv("OAUTH_JWKS_CACHE_DURATION", "30m")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://auth.example.com/.well-known/jwks.json", config.OAuthJWKSURL)
	require.Equal(t, "https://auth.example.com", config.OAuthIssuer)
	require.Equal(t, "dashboard-api", config.OAuthAudience)
	require.Equal(t, "https://e2b.dev/user_id", config.OAuthUserIDClaim)
	require.Equal(t, "preferred_email", config.OAuthEmailClaim)
	require.Equal(t, 30*time.Minute, config.OAuthJWKSCacheDuration)
}
