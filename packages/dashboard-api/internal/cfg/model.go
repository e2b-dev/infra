package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	Port                       int      `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString   string   `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	SupabaseJWTSecrets         []string `env:"SUPABASE_JWT_SECRETS"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`

	AuthUserSyncBackgroundWorkerEnabled bool `env:"AUTH_USER_SYNC_BACKGROUND_WORKER_ENABLED" envDefault:"false"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	return config, err
}
