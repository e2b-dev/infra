package service_discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	DnsProviderKey    = "DNS"
	StaticProviderKey = "STATIC"
	NomadProvider     = "NOMAD"
	K8sPodsProvider   = "K8S-PODS"
)

func BuildServiceDiscoveryProvider(ctx context.Context, config cfg.ServiceDiscoveryConfig, port uint16, logger logger.Logger) (ServiceDiscoveryAdapter, error) {
	switch strings.ToUpper(config.Provider) {
	case DnsProviderKey:
		return createDnsProvider(ctx, config, port, logger)
	case K8sPodsProvider:
		return createK8sProvider(ctx, config, port, logger)
	case NomadProvider:
		return createNomadProvider(ctx, config, port, logger)
	case StaticProviderKey:
		return createStaticProvider(config, port)
	default:
		return nil, fmt.Errorf("unsupported service discovery provider: %s", config.Provider)
	}
}

var (
	ErrMissingDNSResolver = errors.New("missing DNS resolver address")
	ErrMissingDNSQuery    = errors.New("missing DNS query")
)

func createDnsProvider(ctx context.Context, config cfg.ServiceDiscoveryConfig, port uint16, logger logger.Logger) (ServiceDiscoveryAdapter, error) {
	dnsResolverAddress := config.DNSResolverAddress
	if dnsResolverAddress == "" {
		return nil, ErrMissingDNSResolver
	}

	dnsHosts := config.DNSQuery
	if len(dnsHosts) == 0 {
		return nil, ErrMissingDNSQuery
	}

	return NewDnsServiceDiscovery(ctx, logger, dnsHosts, dnsResolverAddress, port), nil
}

var (
	ErrMissingPodNamespace = errors.New("missing pod namespace")
	ErrMissingPodLabels    = errors.New("missing pod labels")
)

func createK8sProvider(ctx context.Context, config cfg.ServiceDiscoveryConfig, port uint16, logger logger.Logger) (ServiceDiscoveryAdapter, error) {
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

	return NewK8sServiceDiscovery(ctx, logger, client, port, podLabels, podNamespace, hostIP), nil
}

var (
	ErrMissingNomadEndpoint  = errors.New("missing nomad endpoint")
	ErrMissingNomadToken     = errors.New("missing nomad token")
	ErrMissingNomadJobPrefix = errors.New("missing nomad job prefix")
)

func createNomadProvider(ctx context.Context, config cfg.ServiceDiscoveryConfig, port uint16, logger logger.Logger) (ServiceDiscoveryAdapter, error) {
	nomadEndpoint := config.NomadEndpoint
	if nomadEndpoint == "" {
		return nil, ErrMissingNomadEndpoint
	}

	nomadToken := config.NomadToken
	if nomadToken == "" {
		return nil, ErrMissingNomadToken
	}

	jobPrefix := config.NomadJobPrefix
	if jobPrefix == "" {
		return nil, ErrMissingNomadJobPrefix
	}

	return NewNomadServiceDiscovery(ctx, logger, port, nomadEndpoint, nomadToken, jobPrefix)
}

var ErrMissingStaticEndpoints = errors.New("missing static endpoints")

func createStaticProvider(config cfg.ServiceDiscoveryConfig, port uint16) (ServiceDiscoveryAdapter, error) {
	static := config.StaticEndpoints
	if len(static) == 0 {
		return nil, ErrMissingStaticEndpoints
	}

	return NewStaticServiceDiscovery(static, port), nil
}
