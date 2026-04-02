package cfg

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port                       int      `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString   string   `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	SupabaseJWTSecrets         []string `env:"SUPABASE_JWT_SECRETS"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if err == nil && config.RedisURL == "" && config.RedisClusterURL == "" {
		err = fmt.Errorf("at least one of REDIS_URL or REDIS_CLUSTER_URL must be set")
	}

	return config, err
}
