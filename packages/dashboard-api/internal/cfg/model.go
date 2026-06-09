package cfg

import (
	"errors"
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

type FailureCondition string

const (
	FailureConditionMissingRedisConnection FailureCondition = "missing_redis_connection"
	FailureConditionMissingOrySDKURL       FailureCondition = "missing_ory_sdk_url"
	FailureConditionMissingOryProjectToken FailureCondition = "missing_ory_project_api_token"
	FailureConditionMissingOryIssuerURL    FailureCondition = "missing_ory_issuer_url"
	FailureConditionOryIssuerURLMismatch   FailureCondition = "ory_issuer_url_mismatch"
)

type FailureError struct {
	Condition FailureCondition
	err       error
}

func (e *FailureError) Error() string {
	return e.err.Error()
}

func (e *FailureError) Unwrap() error {
	return e.err
}

func ParseFailureCondition(err error) (FailureCondition, bool) {
	failureErr, ok := errors.AsType[*FailureError](err)
	if !ok {
		return "", false
	}

	return failureErr.Condition, true
}

func newFailureError(condition FailureCondition, message string) error {
	return &FailureError{
		Condition: condition,
		err:       errors.New(message),
	}
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
		err = newFailureError(FailureConditionMissingRedisConnection, "at least one of REDIS_URL or REDIS_CLUSTER_URL must be set")
	}

	if err == nil {
		err = validateUserProfileProvider(&config)
	}

	return config, err
}

// ORY_ISSUER_URL must match the auth-provider's JWT issuer: the Ory profile
// provider filters public.user_identities by oidc_iss, so a mismatch against
// the iss claim stored at bootstrap silently strands every user.
func validateUserProfileProvider(config *Config) error {
	if !config.UserProfileProvider.RequiresOry() {
		return nil
	}

	if config.OrySDKURL == "" {
		return newFailureError(FailureConditionMissingOrySDKURL, "ORY_SDK_URL is required when USER_PROFILE_PROVIDER uses ory")
	}
	if config.OryProjectAPIToken == "" {
		return newFailureError(FailureConditionMissingOryProjectToken, "ORY_PROJECT_API_TOKEN is required when USER_PROFILE_PROVIDER uses ory")
	}

	if config.OryIssuerURL == "" && len(config.AuthProvider.JWT) == 1 {
		config.OryIssuerURL = strings.TrimSpace(config.AuthProvider.JWT[0].Issuer.URL)
	}
	if config.OryIssuerURL == "" {
		return newFailureError(FailureConditionMissingOryIssuerURL, "ORY_ISSUER_URL is required when USER_PROFILE_PROVIDER uses ory")
	}

	if len(config.AuthProvider.JWT) > 0 {
		for _, jwt := range config.AuthProvider.JWT {
			if strings.TrimSpace(jwt.Issuer.URL) == config.OryIssuerURL {
				return nil
			}
		}

		return newFailureError(FailureConditionOryIssuerURLMismatch, "ORY_ISSUER_URL does not match any AUTH_PROVIDER_CONFIG.jwt[].issuer.url; identities stored at bootstrap would be invisible to the Ory profile provider")
	}

	return nil
}
