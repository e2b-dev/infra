package cfg

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
)

type Config struct {
	Port                       int                 `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString   string              `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string              `env:"CLICKHOUSE_CONNECTION_STRING"`
	AdminToken                 string              `env:"ADMIN_TOKEN,required,notEmpty"`
	AuthProvider               auth.ProviderConfig `env:"AUTH_PROVIDER_CONFIG"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`
	SupabaseDBConnectionString        string `env:"SUPABASE_DB_CONNECTION_STRING"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	BillingServerURL      string `env:"BILLING_SERVER_URL"`
	BillingServerAPIToken string `env:"BILLING_SERVER_API_TOKEN"`

	UserProfileProvider userprofile.Mode `env:"USER_PROFILE_PROVIDER"       envDefault:"supabase"`
	OrySDKURL           string           `env:"ORY_SDK_URL"`
	OryProjectAPIToken  string           `env:"ORY_PROJECT_API_TOKEN,unset"`
	OryIssuerURL        string           `env:"ORY_ISSUER_URL"`
}

func Parse() (Config, error) {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeFor[auth.ProviderConfig](): func(v string) (any, error) {
				return auth.ParseProviderConfig(v)
			},
			reflect.TypeFor[userprofile.Mode](): func(v string) (any, error) {
				return userprofile.ParseMode(v)
			},
		},
	})

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if config.SupabaseDBConnectionString == "" {
		config.SupabaseDBConnectionString = config.PostgresConnectionString
	}

	if err == nil && config.RedisURL == "" && config.RedisClusterURL == "" {
		err = errors.New("at least one of REDIS_URL or REDIS_CLUSTER_URL must be set")
	}

	if err == nil {
		err = validateUserProfileProvider(&config)
	}

	return config, err
}

// validateUserProfileProvider enforces Ory-specific env requirements and keeps
// ORY_ISSUER_URL aligned with the configured auth-provider issuer. The Ory
// profile provider filters public.user_identities by oidc_iss, so a mismatch
// against the iss claim stored at bootstrap silently strands every user.
func validateUserProfileProvider(config *Config) error {
	if !config.UserProfileProvider.RequiresOry() {
		return nil
	}

	if config.OrySDKURL == "" {
		return errors.New("ORY_SDK_URL is required when USER_PROFILE_PROVIDER uses ory")
	}
	if config.OryProjectAPIToken == "" {
		return errors.New("ORY_PROJECT_API_TOKEN is required when USER_PROFILE_PROVIDER uses ory")
	}

	if config.OryIssuerURL == "" && len(config.AuthProvider.JWT) == 1 {
		config.OryIssuerURL = strings.TrimSpace(config.AuthProvider.JWT[0].Issuer.URL)
	}
	if config.OryIssuerURL == "" {
		return errors.New("ORY_ISSUER_URL is required when USER_PROFILE_PROVIDER uses ory")
	}

	if len(config.AuthProvider.JWT) > 0 {
		for _, jwt := range config.AuthProvider.JWT {
			if strings.TrimSpace(jwt.Issuer.URL) == config.OryIssuerURL {
				return nil
			}
		}

		return fmt.Errorf("ORY_ISSUER_URL %q does not match any AUTH_PROVIDER_CONFIG.jwt[].issuer.url; identities stored at bootstrap would be invisible to the Ory profile provider", config.OryIssuerURL)
	}

	return nil
}
