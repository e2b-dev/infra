package nodediscovery

type Config struct {
	Provider         string `env:"PROVIDER,required"`
	OrchestratorPort uint16 `env:"PORT"              envDefault:"5008"`

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
	NomadEndpoint string `env:"NOMAD_ENDPOINT"`
	NomadToken    string `env:"NOMAD_TOKEN"`
}
