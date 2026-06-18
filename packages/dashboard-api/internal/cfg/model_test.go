package cfg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

func setBaseEnv(t *testing.T) {
	t.Helper()

	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
	t.Setenv("ORY_SDK_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("ORY_PROJECT_API_TOKEN", "pat")
	t.Setenv("ORY_ISSUER_URL", "https://auth.example.com")
}

func TestParseAuthProviderConfig(t *testing.T) {
	setBaseEnv(t)
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

func TestParseRequiresOryEnv(t *testing.T) {
	tests := []struct {
		name          string
		sdkURL        string
		token         string
		issuer        string
		wantErrSubstr string
	}{
		{
			name:          "without sdk url errors",
			token:         "pat",
			issuer:        "https://auth.example.com",
			wantErrSubstr: "ORY_SDK_URL",
		},
		{
			name:          "without token errors",
			sdkURL:        "https://tenant.projects.oryapis.com",
			issuer:        "https://auth.example.com",
			wantErrSubstr: "ORY_PROJECT_API_TOKEN",
		},
		{
			name:          "without issuer errors",
			sdkURL:        "https://tenant.projects.oryapis.com",
			token:         "pat",
			wantErrSubstr: "ORY_ISSUER_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
			t.Setenv("ADMIN_TOKEN", "admin-token")
			t.Setenv("REDIS_URL", "redis://example")
			t.Setenv("ORY_SDK_URL", tt.sdkURL)
			t.Setenv("ORY_PROJECT_API_TOKEN", tt.token)
			t.Setenv("ORY_ISSUER_URL", tt.issuer)

			_, err := Parse()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErrSubstr)
		})
	}
}

func TestParseOryHappyPathIsIndependentOfAuthProvider(t *testing.T) { //nolint:paralleltest // t.Setenv cannot be used with t.Parallel.
	setBaseEnv(t)

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "https://tenant.projects.oryapis.com", config.OrySDKURL)
	require.Equal(t, "pat", config.OryProjectAPIToken)
	require.Equal(t, "https://auth.example.com", config.OryIssuerURL)
	require.Empty(t, config.AuthProvider.JWT)
}

func TestParseOryIssuerDefaultsFromSingleAuthProviderJWT(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://example")
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("REDIS_URL", "redis://example")
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

func TestParseOryIssuerRejectsMismatchAgainstAuthProvider(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ORY_ISSUER_URL", "https://tenant.projects.oryapis.com")
	t.Setenv("AUTH_PROVIDER_CONFIG", `{
		"jwt": [
			{"issuer": {"url": "https://auth.mycompany.com", "audiences": ["dashboard-api"]}}
		]
	}`)

	_, err := Parse()
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match any AUTH_PROVIDER_CONFIG")
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
