package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	HealthPort uint16 `env:"HEALTH_PORT" envDefault:"3003"`
	ProxyPort  uint16 `env:"PROXY_PORT"  envDefault:"3002"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	ApiGrpcAddress string `env:"API_GRPC_ADDRESS"`
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{})
}
