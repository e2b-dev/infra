package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	HealthPort uint16 `env:"HEALTH_PORT" envDefault:"3003"`
	ProxyPort  uint16 `env:"PROXY_PORT"  envDefault:"3002"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
	RedisPoolSize    int    `env:"REDIS_POOL_SIZE"     envDefault:"40"`

	APIInternalGRPCAddress     string `env:"API_INTERNAL_GRPC_ADDRESS"`
	ControlPlaneAPIGRPCAddress string `env:"CONTROL_PLANE_API_GRPC_ADDRESS"`

	ControlPlaneAPIGRPCOAuthClientID     string `env:"CONTROL_PLANE_API_GRPC_OAUTH_CLIENT_ID"`
	ControlPlaneAPIGRPCOAuthClientSecret string `env:"CONTROL_PLANE_API_GRPC_OAUTH_CLIENT_SECRET"`
	ControlPlaneAPIGRPCOAuthTokenURL     string `env:"CONTROL_PLANE_API_GRPC_OAUTH_TOKEN_URL"`
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{})
}
