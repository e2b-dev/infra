package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	AdminToken string `env:"ADMIN_TOKEN"`

	AnalyticsCollectorAPIToken string `env:"ANALYTICS_COLLECTOR_API_TOKEN"`
	AnalyticsCollectorHost     string `env:"ANALYTICS_COLLECTOR_HOST"`

	ClickhouseConnectionString string `env:"CLICKHOUSE_CONNECTION_STRING"`

	DNSPort uint16 `env:"DNS_PORT" envDefault:"5353"`

	LocalClusterEndpoint string `env:"LOCAL_CLUSTER_ENDPOINT"`
	LocalClusterToken    string `env:"LOCAL_CLUSTER_TOKEN"`

	NomadAddress string `env:"NOMAD_ADDRESS" envDefault:"http://localhost:4646"`
	NomadToken   string `env:"NOMAD_TOKEN"`

	PostgresConnectionString   string `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	PostgresMaxConnections     int    `env:"POSTGRES_MAX_CONNECTIONS"                     envDefault:"40"`
	PostgresMinIdleConnections int    `env:"POSTGRES_MIN_IDLE_CONNECTIONS"                envDefault:"5"`

	PosthogAPIKey string `env:"POSTHOG_API_KEY"`

	RedisURL        string `env:"REDIS_URL"`
	RedisClusterURL string `env:"REDIS_CLUSTER_URL"`

	SandboxAccessTokenHashSeed string `env:"SANDBOX_ACCESS_TOKEN_HASH_SEED"`

	// SupabaseJWTSecrets is a list of secrets used to verify the Supabase JWT.
	// More secrets are possible in the case of JWT secret rotation where we need to accept
	// tokens signed with the old secret for some time.
	SupabaseJWTSecrets []string `env:"SUPABASE_JWT_SECRETS"`

	TemplateManagerHost string `env:"TEMPLATE_MANAGER_HOST"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)
	return config, err
}
