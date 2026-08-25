package nodediscovery

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	DnsProviderKey    = "DNS"
	StaticProviderKey = "STATIC"
	NomadProvider     = "NOMAD"
	K8sPodsProvider   = "K8S-PODS"
)

// BuildServiceDiscoveryProvider selects a cached adapter from configuration;
// the query adapters have no config shape and are constructed directly.
func BuildServiceDiscoveryProvider(config Config, logger logger.Logger) (Discovery, error) {
	switch strings.ToUpper(config.Provider) {
	case DnsProviderKey:
		return createDnsProvider(config, logger)
	case K8sPodsProvider:
		return createK8sProvider(config, logger)
	case NomadProvider:
		return createNomadProvider(config, logger)
	case StaticProviderKey:
		return createStaticProvider(config)
	default:
		return nil, fmt.Errorf("unsupported service discovery provider: %s", config.Provider)
	}
}

var (
	ErrMissingDNSResolver = errors.New("missing DNS resolver address")
	ErrMissingDNSQuery    = errors.New("missing DNS query")
)

func createDnsProvider(config Config, logger logger.Logger) (Discovery, error) {
	dnsResolverAddress := config.DNSResolverAddress
	if dnsResolverAddress == "" {
		return nil, ErrMissingDNSResolver
	}

	dnsHosts := config.DNSQuery
	if len(dnsHosts) == 0 {
		return nil, ErrMissingDNSQuery
	}

	return NewDnsServiceDiscovery(logger, dnsHosts, dnsResolverAddress, config.OrchestratorPort), nil
}

var (
	ErrMissingPodNamespace = errors.New("missing pod namespace")
	ErrMissingPodLabels    = errors.New("missing pod labels")
)

func createK8sProvider(config Config, logger logger.Logger) (Discovery, error) {
	podNamespace := config.PodNamespace
	if podNamespace == "" {
		return nil, ErrMissingPodNamespace
	}

	podLabels := config.PodLabels
	if podLabels == "" {
		return nil, ErrMissingPodLabels
	}

	// Allow to optionally switch and use HostIP as service discovery entry
	hostIP := config.HostIP

	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build in-cluster config: %w", err)
	}

	client, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build in-cluster client: %w", err)
	}

	return NewK8sServiceDiscovery(logger, client, config.OrchestratorPort, podLabels, podNamespace, hostIP), nil
}

var (
	ErrMissingNomadEndpoint = errors.New("missing nomad endpoint")
	ErrMissingNomadToken    = errors.New("missing nomad token")
)

func createNomadProvider(config Config, logger logger.Logger) (Discovery, error) {
	nomadEndpoint := config.NomadEndpoint
	if nomadEndpoint == "" {
		return nil, ErrMissingNomadEndpoint
	}

	nomadToken := config.NomadToken
	if nomadToken == "" {
		return nil, ErrMissingNomadToken
	}

	return NewNomadServiceDiscovery(logger, config.OrchestratorPort, nomadEndpoint, nomadToken)
}

var ErrMissingStaticEndpoints = errors.New("missing static endpoints")

func createStaticProvider(config Config) (Discovery, error) {
	static := config.StaticEndpoints
	if len(static) == 0 {
		return nil, ErrMissingStaticEndpoints
	}

	return NewStaticServiceDiscovery(static, config.OrchestratorPort), nil
}
