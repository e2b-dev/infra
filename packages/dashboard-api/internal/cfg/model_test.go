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
			name:          "ory mode without sdk url or issuer errors on sdk url",
			mode:          "ory",
			token:         "pat",
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
	t.Setenv("USER_PROFILE_PROVIDER", "ory")
	t.Setenv("ORY_SDK_URL", "https://ory.example.test")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("ORY_ISSUER_URL", "https://ory.example.test")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, userprofile.ModeOry, config.UserProfileProvider)
	require.Equal(t, "https://ory.example.test", config.OrySDKURL)
	require.Equal(t, "pat", config.OryProjectAPIToken)
	require.Equal(t, "https://ory.example.test", config.OryIssuerURL)
	require.Empty(t, config.AuthProvider.JWT)
}

func TestParseUserProfileProviderOryIssuerRequiredWhenAuthProviderEmpty(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "ory")
	t.Setenv("ORY_SDK_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")

	_, err := Parse()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ORY_ISSUER_URL")
}

func TestParseUserProfileProviderOryIssuerDefaultsFromSingleAuthProviderJWT(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "ory")
	t.Setenv("ORY_SDK_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"jwt": [
			{"issuer": {"url": "https://auth.mycompany.com", "audiences": ["dashboard-api"]}}
		]
	}`)

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://auth.mycompany.com", config.OryIssuerURL)
}

func TestParseUserProfileProviderOryIssuerRejectsMismatchAgainstAuthProvider(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "ory")
	t.Setenv("ORY_SDK_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("ORY_ISSUER_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"jwt": [
			{"issuer": {"url": "https://auth.mycompany.com", "audiences": ["dashboard-api"]}}
		]
	}`)

	_, err := Parse()
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match any AUTH_PROVIDER_CONFIG")
	require.NotContains(t, err.Error(), "https://tenant.projects.oryapis.com")
}

func TestParseUserProfileProviderOryIssuerOverrideWithoutAuthProvider(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("USER_PROFILE_PROVIDER", "ory")
	t.Setenv("ORY_SDK_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("ORY_ISSUER_URL", "https://auth.mycompany.com")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://tenant.projects.oryapis.com", config.OrySDKURL)
	require.Equal(t, "https://auth.mycompany.com", config.OryIssuerURL)
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

func TestParseFailureCondition(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want FailureCondition
	}{
		{
			name: "missing redis connection",
			env: map[string]string{
				"POSTGRES_CONNECTION_STRING": "postgres://example",
				"ADMIN_TOKEN":                "admin-token",
			},
			want: FailureConditionMissingRedisConnection,
		},
		{
			name: "missing ory sdk url",
			env: map[string]string{
				"POSTGRES_CONNECTION_STRING": "postgres://example",
				"ADMIN_TOKEN":                "admin-token",
				"REDIS_URL":                  "redis://example",
				"USER_PROFILE_PROVIDER":      "ory",
				"ORY_PROJECT_API_TOKEN":      "pat",
				"ORY_ISSUER_URL":             "https://auth.example.com",
			},
			want: FailureConditionMissingOrySDKURL,
		},
		{
			name: "missing ory project token",
			env: map[string]string{
				"POSTGRES_CONNECTION_STRING": "postgres://example",
				"ADMIN_TOKEN":                "admin-token",
				"REDIS_URL":                  "redis://example",
				"USER_PROFILE_PROVIDER":      "ory",
				"ORY_SDK_URL":                "https://tenant.projects.oryapis.com",
				"ORY_ISSUER_URL":             "https://auth.example.com",
			},
			want: FailureConditionMissingOryProjectToken,
		},
		{
			name: "missing ory issuer url",
			env: map[string]string{
				"POSTGRES_CONNECTION_STRING": "postgres://example",
				"ADMIN_TOKEN":                "admin-token",
				"REDIS_URL":                  "redis://example",
				"USER_PROFILE_PROVIDER":      "ory",
				"ORY_SDK_URL":                "https://tenant.projects.oryapis.com",
				"ORY_PROJECT_API_TOKEN":      "pat",
			},
			want: FailureConditionMissingOryIssuerURL,
		},
		{
			name: "ory issuer url mismatch",
			env: map[string]string{
				"POSTGRES_CONNECTION_STRING": "postgres://example",
				"ADMIN_TOKEN":                "admin-token",
				"REDIS_URL":                  "redis://example",
				"USER_PROFILE_PROVIDER":      "ory",
				"ORY_SDK_URL":                "https://tenant.projects.oryapis.com",
				"ORY_PROJECT_API_TOKEN":      "pat",
				"ORY_ISSUER_URL":             "https://tenant.projects.oryapis.com",
				"AUTH_PROVIDER_CONFIG": `{
					"jwt": [
						{"issuer": {"url": "https://auth.example.com", "audiences": ["dashboard-api"]}}
					]
				}`,
			},
			want: FailureConditionOryIssuerURLMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := Parse()
			require.Error(t, err)

			got, ok := ParseFailureCondition(err)
			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
