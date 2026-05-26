package cfg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
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
				"cacheDuration": "30m"
			}
		]
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.Len(t, config.AuthProvider.JWT, 1)

	entry := config.AuthProvider.JWT[0]
	require.Equal(t, "https://auth.example.com", entry.Issuer.URL)
	require.Equal(t, []string{"dashboard-api", "other"}, entry.Issuer.Audiences)
	require.Equal(t, oidc.AudienceMatchAny, entry.Issuer.AudienceMatchPolicy)
	require.Equal(t, 30*time.Minute, entry.CacheDuration)
}

func TestParseAuthProviderConfigLegacy(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"legacy": {
			"hmac": { "secrets": ["secret-1", "secret-2"] }
		}
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.NotNil(t, config.AuthProvider.Legacy)
	require.Equal(t, []string{"secret-1", "secret-2"}, config.AuthProvider.Legacy.HMAC.Secrets)
}

func TestParseUserProfileProviderDefaultsToSupabase(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, userprofile.ModeSupabase, config.UserProfileProvider)
}

func TestParseUserProfileProviderOryRequiresOryEnv(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		sdkURL        string
		token         string
		issuer        string
		wantErrSubstr string
	}{
		{
			name:          "ory mode without sdk url errors",
			mode:          "ory",
			token:         "pat",
			issuer:        "https://ory.example.test",
			wantErrSubstr: "ORY_SDK_URL",
		},
		{
			name:          "ory mode without token errors",
			mode:          "ory",
			sdkURL:        "https://ory.example.test",
			issuer:        "https://ory.example.test",
			wantErrSubstr: "ORY_PROJECT_API_TOKEN",
		},
		{
			name:          "ory mode without issuer errors",
			mode:          "ory",
			sdkURL:        "https://ory.example.test",
			token:         "pat",
			wantErrSubstr: "ORY_ISSUER_URL",
		},
		{
			name:          "fallback mode applies same requirements",
			mode:          "supabase-ory-fallback",
			token:         "pat",
			issuer:        "https://ory.example.test",
			wantErrSubstr: "ORY_SDK_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
			t.Setenv("ADMIN_TOKEN", "admin-token")
			t.Setenv("REDIS_URL", "redis://example")
			t.Setenv("USER_PROFILE_PROVIDER", tt.mode)
			t.Setenv("ORY_SDK_URL", tt.sdkURL)
			t.Setenv("ORY_PROJECT_API_TOKEN", tt.token)
			t.Setenv("ORY_ISSUER_URL", tt.issuer)

			_, err := Parse()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErrSubstr)
		})
	}
}

func TestParseUserProfileProviderOryHappyPathIsIndependentOfAuthProvider(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "supabase-ory-fallback")
	t.Setenv("ORY_SDK_URL", "https://ory.example.test")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("ORY_ISSUER_URL", "https://ory.example.test")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, userprofile.ModeSupabaseOryFallback, config.UserProfileProvider)
	require.Equal(t, "https://ory.example.test", config.OrySDKURL)
	require.Equal(t, "pat", config.OryProjectAPIToken)
	require.Equal(t, "https://ory.example.test", config.OryIssuerURL)
	require.Empty(t, config.AuthProvider.JWT)
}

func TestParseUserProfileProviderInvalidModeErrors(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "totally-invalid")

	_, err := Parse()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user profile provider")
}
