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

func TestParseUserProfileProviderOryRequiresEnvAndIssuer(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		sdkURL        string
		token         string
		authConfig    string
		wantErrSubstr string
	}{
		{
			name:          "ory mode without sdk url errors",
			mode:          "ory",
			token:         "pat",
			authConfig:    oryJWTConfigJSON,
			wantErrSubstr: "ORY_SDK_URL",
		},
		{
			name:          "ory mode without token errors",
			mode:          "ory",
			sdkURL:        "https://ory.example.test",
			authConfig:    oryJWTConfigJSON,
			wantErrSubstr: "ORY_PROJECT_API_TOKEN",
		},
		{
			name:          "ory mode without jwt issuer errors",
			mode:          "ory",
			sdkURL:        "https://ory.example.test",
			token:         "pat",
			wantErrSubstr: "AUTH_PROVIDER_CONFIG must declare exactly one jwt issuer",
		},
		{
			name:          "fallback mode applies same requirements",
			mode:          "supabase-ory-fallback",
			token:         "pat",
			authConfig:    oryJWTConfigJSON,
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
			t.Setenv("AUTH_PROVIDER_CONFIG", tt.authConfig)

			_, err := Parse()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErrSubstr)
		})
	}
}

func TestParseUserProfileProviderOryHappyPath(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "supabase-ory-fallback")
	t.Setenv("ORY_SDK_URL", "https://ory.example.test")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("AUTH_PROVIDER_CONFIG", oryJWTConfigJSON)

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, userprofile.ModeSupabaseOryFallback, config.UserProfileProvider)
	require.Equal(t, "https://ory.example.test", config.OrySDKURL)
	require.Equal(t, "pat", config.OryProjectAPIToken)
	require.Len(t, config.AuthProvider.JWT, 1)
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

const oryJWTConfigJSON = `{
		"jwt": [
			{
				"issuer": {
					"url": "https://ory.example.test",
					"audiences": ["dashboard-api"]
				}
			}
		]
	}`
