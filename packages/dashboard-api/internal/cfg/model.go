package cfg

import (
	"errors"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port                       int           `env:"PORT"                                         envDefault:"3010"`
	PostgresConnectionString   string        `env:"POSTGRES_CONNECTION_STRING,required,notEmpty"`
	ClickhouseConnectionString string        `env:"CLICKHOUSE_CONNECTION_STRING"`
	AdminToken                 string        `env:"ADMIN_TOKEN,required,notEmpty"`
	SupabaseJWTSecrets         []string      `env:"SUPABASE_JWT_SECRETS"`
	OAuthJWKSURL               string        `env:"OAUTH_JWKS_URL"`
	OAuthIssuer                string        `env:"OAUTH_ISSUER"`
	OAuthAudience              string        `env:"OAUTH_AUDIENCE"`
	OAuthUserIDClaim           string        `env:"OAUTH_USER_ID_CLAIM"     envDefault:"sub"`
	OAuthEmailClaim            string        `env:"OAUTH_EMAIL_CLAIM"       envDefault:"email"`
	OAuthJWKSCacheDuration     time.Duration `env:"OAUTH_JWKS_CACHE_DURATION" envDefault:"5m"`

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
	err := env.Parse(&config)

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
