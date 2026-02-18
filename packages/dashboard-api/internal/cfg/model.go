package cfg

import (
	"github.com/caarlos0/env/v11"
)

const MinSupabaseJWTSecretLength = 16

type Config struct {
	Port                       int      `env:"PORT" envDefault:"3010"`
	PostgresConnectionString   string   `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	SupabaseJWTSecrets         []string `env:"SUPABASE_JWT_SECRETS"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	return config, err
}
