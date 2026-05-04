package cfg

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
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

	EnableAuthUserSyncBackgroundWorker bool   `env:"ENABLE_AUTH_USER_SYNC_BACKGROUND_WORKER" envDefault:"false"`
	EnableBillingHTTPTeamProvisionSink bool   `env:"ENABLE_BILLING_HTTP_TEAM_PROVISION_SINK" envDefault:"false"`
	BillingServerURL                   string `env:"BILLING_SERVER_URL"`
	BillingServerAPIToken              string `env:"BILLING_SERVER_API_TOKEN"`
}

func Parse() (Config, error) {
	var config Config
	err := env.ParseWithOptions(&config, env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeFor[auth.ProviderConfig](): func(v string) (any, error) {
				var config auth.ProviderConfig
				if v == "" {
					return config, nil
				}

				if err := json.Unmarshal([]byte(v), &config); err != nil {
					return nil, err
				}

				return config, nil
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

	return config, err
}
