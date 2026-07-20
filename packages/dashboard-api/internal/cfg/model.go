package cfg

import (
	"errors"
	"reflect"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

type Config struct {
	Port                        int                 `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString    string              `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString  string              `env:"CLICKHOUSE_CONNECTION_STRING"`
	ClickhouseConnectionStrings []string            `env:"CLICKHOUSE_CONNECTION_STRINGS"                envSeparator:";"`
	AdminToken                  string              `env:"ADMIN_TOKEN,required,notEmpty"`
	AuthProvider                auth.ProviderConfig `env:"AUTH_PROVIDER_CONFIG"`
	AdminAuth                   auth.ProviderConfig `env:"ADMIN_AUTH_CONFIG"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	BillingServerURL      string `env:"BILLING_SERVER_URL"`
	BillingServerAPIToken string `env:"BILLING_SERVER_API_TOKEN"`

	OrySDKURL          string `env:"ORY_SDK_URL"`
	OryProjectAPIToken string `env:"ORY_PROJECT_API_TOKEN,unset"`

	DomainName string `env:"DOMAIN_NAME" envDefault:""`
}

type FailureCondition string

const (
	FailureConditionMissingRedisConnection FailureCondition = "missing_redis_connection"
	FailureConditionMissingOrySDKURL       FailureCondition = "missing_ory_sdk_url"
	FailureConditionMissingOryProjectToken FailureCondition = "missing_ory_project_api_token"
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
	var failureErr *FailureError
	if !errors.As(err, &failureErr) {
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
		},
	})

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if err == nil && config.RedisURL == "" && config.RedisClusterURL == "" {
		err = newFailureError(FailureConditionMissingRedisConnection, "at least one of REDIS_URL or REDIS_CLUSTER_URL must be set")
	}

	if err == nil {
		err = validateOryConfig(&config)
	}

	return config, err
}

func validateOryConfig(config *Config) error {
	if config.OrySDKURL == "" {
		return newFailureError(FailureConditionMissingOrySDKURL, "ORY_SDK_URL is required")
	}
	if config.OryProjectAPIToken == "" {
		return newFailureError(FailureConditionMissingOryProjectToken, "ORY_PROJECT_API_TOKEN is required")
	}

	return nil
}
