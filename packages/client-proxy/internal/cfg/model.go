package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	HealthPort uint16 `env:"HEALTH_PORT" envDefault:"3003"`
	ProxyPort  uint16 `env:"PROXY_PORT"  envDefault:"3002"`
	TLSPort    uint16 `env:"CLIENT_PROXY_TLS_PORT"`

	TLSCertFile string `env:"CLIENT_PROXY_TLS_CERT_FILE"`
	TLSKeyFile  string `env:"CLIENT_PROXY_TLS_KEY_FILE"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
	RedisPoolSize    int    `env:"REDIS_POOL_SIZE"     envDefault:"40"`

	APIInternalGRPCAddress string `env:"API_INTERNAL_GRPC_ADDRESS"`
	APIEdgeGRPCAddress     string `env:"API_EDGE_GRPC_ADDRESS"`

	APIEdgeGRPCOAuthClientID     string `env:"API_EDGE_GRPC_OAUTH_CLIENT_ID"`
	APIEdgeGRPCOAuthClientSecret string `env:"API_EDGE_GRPC_OAUTH_CLIENT_SECRET"`
	APIEdgeGRPCOAuthTokenURL     string `env:"API_EDGE_GRPC_OAUTH_TOKEN_URL"`
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{})
}
