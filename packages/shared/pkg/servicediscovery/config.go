package servicediscovery

type Config struct {
	// Provider selects the discovery backend. "NOMAD+K8S-PODS" unions the two
	// single-platform ones and needs the configuration of both.
	Provider         string `env:"PROVIDER,required"`
	OrchestratorPort uint16 `env:"PORT"              envDefault:"5008"`

	// when Provider == "DNS"
	DNSQuery           []string `env:"DNS_QUERY"`
	DNSResolverAddress string   `env:"DNS_RESOLVER_ADDRESS"`

	// when Provider == "K8S-PODS" or "NOMAD+K8S-PODS"
	//
	// Empty K8sAPIEndpoint uses the pod's own ServiceAccount and only works
	// inside the cluster; set it to reach a cluster from outside, on Google ADC.
	K8sAPIEndpoint string `env:"K8S_API_ENDPOINT"`
	PodNamespace   string `env:"POD_NAMESPACE"`
	PodLabels      string `env:"POD_LABELS"`
	HostIP         bool   `env:"HOST_IP"`

	// when Provider == "STATIC"
	StaticEndpoints []string `env:"STATIC"`

	// when Provider == "NOMAD" or "NOMAD+K8S-PODS"
	NomadEndpoint string `env:"NOMAD_ENDPOINT"`
	NomadToken    string `env:"NOMAD_TOKEN"`
}
