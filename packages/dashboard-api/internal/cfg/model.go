package cfg

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port                       int      `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString   string   `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	SupabaseJWTSecrets         []string `env:"SUPABASE_JWT_SECRETS"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`

	SupabaseAuthUserSyncEnabled      bool          `env:"SUPABASE_AUTH_USER_SYNC_ENABLED"       envDefault:"false"`
	SupabaseAuthUserSyncBatchSize    int32         `env:"SUPABASE_AUTH_USER_SYNC_BATCH_SIZE"    envDefault:"50"`
	SupabaseAuthUserSyncPollInterval time.Duration `env:"SUPABASE_AUTH_USER_SYNC_POLL_INTERVAL" envDefault:"2s"`
	SupabaseAuthUserSyncLockTimeout  time.Duration `env:"SUPABASE_AUTH_USER_SYNC_LOCK_TIMEOUT"  envDefault:"2m"`
	SupabaseAuthUserSyncMaxAttempts  int32         `env:"SUPABASE_AUTH_USER_SYNC_MAX_ATTEMPTS"  envDefault:"20"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	return config, err
}
