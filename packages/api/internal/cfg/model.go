package cfg

import "github.com/caarlos0/env/v11"

const (
	DefaultKernelVersion = "vmlinux-6.1.102"
	// The Firecracker version the last tag + the short SHA (so we can build our dev previews)
	// TODO: The short tag here has only 7 characters — the one from our build pipeline will likely have exactly 8 so this will break.
	DefaultFirecrackerVersion = "v1.12.1_d990331"
)

type Config struct {
	AdminToken string `env:"ADMIN_TOKEN"`

	AnalyticsCollectorAPIToken string `env:"ANALYTICS_COLLECTOR_API_TOKEN"`
	AnalyticsCollectorHost     string `env:"ANALYTICS_COLLECTOR_HOST"`

	ClickhouseConnectionString string `env:"CLICKHOUSE_CONNECTION_STRING"`

	LocalClusterEndpoint string `env:"LOCAL_CLUSTER_ENDPOINT"`
	LocalClusterToken    string `env:"LOCAL_CLUSTER_TOKEN"`

	NomadAddress string `env:"NOMAD_ADDRESS" envDefault:"http://localhost:4646"`
	NomadToken   string `env:"NOMAD_TOKEN"`

	PostgresConnectionString string `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`

	PosthogAPIKey string `env:"POSTHOG_API_KEY"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	SandboxAccessTokenHashSeed string `env:"SANDBOX_ACCESS_TOKEN_HASH_SEED"`

	// SupabaseJWTSecrets is a list of secrets used to verify the Supabase JWT.
	// More secrets are possible in the case of JWT secret rotation where we need to accept
	// tokens signed with the old secret for some time.
	SupabaseJWTSecrets []string `env:"SUPABASE_JWT_SECRETS"`

	TemplateManagerHost string `env:"TEMPLATE_MANAGER_HOST"`

	DefaultKernelVersion      string `env:"DEFAULT_KERNEL_VERSION"`
	DefaultFirecrackerVersion string `env:"DEFAULT_FIRECRACKER_VERSION"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)

	if config.DefaultKernelVersion == "" {
		config.DefaultKernelVersion = DefaultKernelVersion
	}

	if config.DefaultFirecrackerVersion == "" {
		config.DefaultFirecrackerVersion = DefaultFirecrackerVersion
	}

	return config, err
}
