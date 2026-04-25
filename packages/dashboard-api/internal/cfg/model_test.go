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
	t.Setenv("AUTH_PROVIDER_JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
	t.Setenv("AUTH_PROVIDER_JWT_ISSUER", "https://auth.example.com")
	t.Setenv("AUTH_PROVIDER_JWT_AUDIENCE", "dashboard-api")
	t.Setenv("AUTH_PROVIDER_JWT_USER_ID_CLAIM", "https://e2b.dev/user_id")
	t.Setenv("AUTH_PROVIDER_JWT_EMAIL_CLAIM", "preferred_email")
	t.Setenv("AUTH_PROVIDER_JWKS_CACHE_DURATION", "30m")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://auth.example.com/.well-known/jwks.json", config.AuthProvider.JWKSURL)
	require.Equal(t, "https://auth.example.com", config.AuthProvider.Issuer)
	require.Equal(t, "dashboard-api", config.AuthProvider.Audience)
	require.Equal(t, "https://e2b.dev/user_id", config.AuthProvider.UserIDClaim)
	require.Equal(t, "preferred_email", config.AuthProvider.EmailClaim)
	require.Equal(t, 30*time.Minute, config.AuthProvider.JWKSCacheDuration)
}
