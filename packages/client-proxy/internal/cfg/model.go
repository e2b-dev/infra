package cfg

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HealthPort uint16 `env:"HEALTH_PORT" envDefault:"3003"`
	ProxyPort  uint16 `env:"PROXY_PORT"  envDefault:"3002"`
	TLSPort    uint16 `env:"CLIENT_PROXY_TLS_PORT"`

	TLSCertFile string `env:"CLIENT_PROXY_TLS_CERT_FILE"`
	TLSKeyFile  string `env:"CLIENT_PROXY_TLS_KEY_FILE"`

	InternalTLSCAPool                 string        `env:"INTERNAL_TLS_CA_POOL"`
	InternalTLSCertificateAuthorityID string        `env:"INTERNAL_TLS_CA_AUTHORITY"`
	InternalTLSDNSName                string        `env:"INTERNAL_TLS_DNS_NAME"`
	InternalTLSCertificateIDPrefix    string        `env:"INTERNAL_TLS_CERT_ID_PREFIX"`
	InternalTLSCertLifetime           time.Duration `env:"INTERNAL_TLS_CERT_LIFETIME"     envDefault:"2160h"`
	InternalTLSRenewBefore            time.Duration `env:"INTERNAL_TLS_CERT_RENEW_BEFORE" envDefault:"720h"`

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
