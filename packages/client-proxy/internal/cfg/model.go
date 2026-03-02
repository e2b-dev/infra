package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	HealthPort uint16 `env:"HEALTH_PORT" envDefault:"3003"`
	ProxyPort  uint16 `env:"PROXY_PORT"  envDefault:"3002"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`

	ApiGrpcAddress          string `env:"API_GRPC_ADDRESS"`
	ApiGrpcTLSEnabled       bool   `env:"API_GRPC_TLS_ENABLED" envDefault:"false"`
	ApiGrpcTLSServerName    string `env:"API_GRPC_TLS_SERVER_NAME"`
	ApiGrpcTLSCABase64      string `env:"API_GRPC_TLS_CA_BASE64"`
	ApiGrpcTLSClientCertB64 string `env:"API_GRPC_TLS_CLIENT_CERT_BASE64"`
	ApiGrpcTLSClientKeyB64  string `env:"API_GRPC_TLS_CLIENT_KEY_BASE64"`
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{})
}
