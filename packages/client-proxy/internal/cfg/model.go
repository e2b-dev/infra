package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	EdgePort                              uint16                 `env:"EDGE_PORT"                                 envDefault:"3001"`
	EdgeSecret                            string                 `env:"EDGE_SECRET"`
	LokiPassword                          string                 `env:"LOKI_PASSWORD"`
	LokiURL                               string                 `env:"LOKI_URL,required"`
	LokiUser                              string                 `env:"LOKI_USER"`
	OrchestratorPort                      uint16                 `env:"ORCHESTRATOR_PORT"                         envDefault:"5008"`
	ProxyPort                             uint16                 `env:"PROXY_PORT"                                envDefault:"3002"`
	SkipInitialOrchestratorReadinessCheck bool                   `env:"SKIP_INITIAL_ORCHESTRATOR_READINESS_CHECK"`
	EdgeServiceDiscovery                  ServiceDiscoveryConfig `envPrefix:"SD_EDGE_"`
	OrchestratorServiceDiscovery          ServiceDiscoveryConfig `envPrefix:"SD_ORCHESTRATOR_"`

	RedisURL         string `env:"REDIS_URL"`
	RedisClusterURL  string `env:"REDIS_CLUSTER_URL"`
	RedisTLSCABase64 string `env:"REDIS_TLS_CA_BASE64"`
}

type ServiceDiscoveryConfig struct {
	Provider string `env:"PROVIDER,required"`

	// when Provider == "DNS"
	DNSQuery           []string `env:"DNS_QUERY"`
	DNSResolverAddress string   `env:"DNS_RESOLVER_ADDRESS"`

	// when Provider == "K8S-PODS"
	PodNamespace string `env:"POD_NAMESPACE"`
	PodLabels    string `env:"POD_LABELS"`
	HostIP       bool   `env:"HOST_IP"`

	// when Provider == "STATIC"
	StaticEndpoints []string `env:"STATIC"`

	// when Provider == "NOMAD"
	NomadEndpoint  string `env:"NOMAD_ENDPOINT"`
	NomadToken     string `env:"NOMAD_TOKEN"`
	NomadJobPrefix string `env:"NOMAD_JOB_PREFIX"`
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{})
}
