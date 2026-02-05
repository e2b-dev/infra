package cfg

import (
	"fmt"
	"os"

	"github.com/caarlos0/env/v11"
)

const (
	DefaultKernelVersion = "vmlinux-6.1.158"
)

type Config struct {
	AdminToken string `env:"ADMIN_TOKEN"`

	AnalyticsCollectorAPIToken string `env:"ANALYTICS_COLLECTOR_API_TOKEN"`
	AnalyticsCollectorHost     string `env:"ANALYTICS_COLLECTOR_HOST"`

	ClickhouseConnectionString string `env:"CLICKHOUSE_CONNECTION_STRING"`

	LokiPassword string `env:"LOKI_PASSWORD"`
	LokiURL      string `env:"LOKI_URL,required"`
	LokiUser     string `env:"LOKI_USER"`

	NomadAddress string `env:"NOMAD_ADDRESS" envDefault:"http://localhost:4646"`
	NomadToken   string `env:"NOMAD_TOKEN"`

	PostgresConnectionString string `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`

	AuthDBConnectionString            string `env:"AUTH_DB_CONNECTION_STRING"`
	AuthDBReadReplicaConnectionString string `env:"AUTH_DB_READ_REPLICA_CONNECTION_STRING"`
	AuthDBMinIdleConnections          int32  `env:"AUTH_DB_MIN_IDLE_CONNECTIONS"           envDefault:"5"`
	AuthDBMaxOpenConnections          int32  `env:"AUTH_DB_MAX_OPEN_CONNECTIONS"           envDefault:"20"`

	PosthogAPIKey string `env:"POSTHOG_API_KEY"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	SandboxAccessTokenHashSeed string `env:"SANDBOX_ACCESS_TOKEN_HASH_SEED"`

	// SupabaseJWTSecrets is a list of secrets used to verify the Supabase JWT.
	// More secrets are possible in the case of JWT secret rotation where we need to accept
	// tokens signed with the old secret for some time.
	SupabaseJWTSecrets []string `env:"SUPABASE_JWT_SECRETS"`

	DefaultKernelVersion string `env:"DEFAULT_KERNEL_VERSION"`

	DefaultPersistentVolumeType string            `env:"DEFAULT_PERSISTENT_VOLUME_TYPE"`
	PersistentVolumeMounts      map[string]string `env:"PERSISTENT_VOLUME_MOUNTS"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	if config.DefaultKernelVersion == "" {
		config.DefaultKernelVersion = DefaultKernelVersion
	}

	if config.AuthDBConnectionString == "" {
		config.AuthDBConnectionString = config.PostgresConnectionString
	}

	if config.PersistentVolumeMounts != nil {
		for name, path := range config.PersistentVolumeMounts {
			info, err := os.Stat(path)
			if err != nil {
				return config, fmt.Errorf("failed to stat persistent volume mount %q (%q): %w", name, path, err)
			}

			if !info.IsDir() {
				return config, fmt.Errorf("persistent volume mount %q (%q) is not a directory", name, path)
			}
		}
	}

	if config.DefaultPersistentVolumeType != "" {
		if _, ok := config.PersistentVolumeMounts[config.DefaultPersistentVolumeType]; !ok {
			return config, fmt.Errorf("default persistent volume type %q is not defined", config.DefaultPersistentVolumeType)
		}
	}

	return config, err
}
