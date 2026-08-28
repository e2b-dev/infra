package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery/dns"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery/kube"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery/nomad"
)

const (
	DnsProviderKey       = "DNS"
	StaticProviderKey    = "STATIC"
	NomadProvider        = "NOMAD"
	K8sPodsProvider      = "K8S-PODS"
	NomadK8sPodsProvider = "NOMAD+K8S-PODS"
)

// New selects a cached backend from configuration;
// the query adapters have no config shape and are constructed directly.
// NomadK8sPodsProvider unions two of them and needs the configuration of both.
//
// ctx bounds any credential the backend refreshes, so it has to live as long as
// the caller intends to list.
func New(ctx context.Context, config servicediscovery.Config, logger logger.Logger) (servicediscovery.Discoverer, error) {
	switch strings.ToUpper(config.Provider) {
	case DnsProviderKey:
		return createDnsProvider(config, logger)
	case K8sPodsProvider:
		return createK8sProvider(ctx, config, logger)
	case NomadK8sPodsProvider:
		return createNomadK8sProvider(ctx, config, logger)
	case NomadProvider:
		return createNomadProvider(config, logger)
	case StaticProviderKey:
		return createStaticProvider(config)
	default:
		return nil, fmt.Errorf("unsupported service discovery provider: %s", config.Provider)
	}
}

// createNomadK8sProvider unions the two single-platform backends for the
// coexistence window. Nomad is the primary, so its entries win a dedup conflict.
func createNomadK8sProvider(ctx context.Context, config servicediscovery.Config, logger logger.Logger) (servicediscovery.Discoverer, error) {
	nomad, err := createNomadProvider(config, logger)
	if err != nil {
		return nil, err
	}

	k8s, err := createK8sProvider(ctx, config, logger)
	if err != nil {
		return nil, err
	}

	return servicediscovery.NewMerged(nomad, k8s), nil
}

var (
	ErrMissingDNSResolver = errors.New("missing DNS resolver address")
	ErrMissingDNSQuery    = errors.New("missing DNS query")
)

func createDnsProvider(config servicediscovery.Config, logger logger.Logger) (servicediscovery.Discoverer, error) {
	dnsResolverAddress := config.DNSResolverAddress
	if dnsResolverAddress == "" {
		return nil, ErrMissingDNSResolver
	}

	dnsHosts := config.DNSQuery
	if len(dnsHosts) == 0 {
		return nil, ErrMissingDNSQuery
	}

	return servicediscovery.Cached(dns.New(dnsHosts, dnsResolverAddress, config.OrchestratorPort), logger), nil
}

var (
	ErrMissingPodNamespace = errors.New("missing pod namespace")
	ErrMissingPodLabels    = errors.New("missing pod labels")
)

func createK8sProvider(ctx context.Context, config servicediscovery.Config, logger logger.Logger) (servicediscovery.Discoverer, error) {
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

	client, err := kube.NewClient(ctx, config.K8sAPIEndpoint)
	if err != nil {
		return nil, err
	}

	return servicediscovery.Cached(kube.NewPodsOnPort(client, podNamespace, podLabels, config.OrchestratorPort, hostIP), logger), nil
}

var (
	ErrMissingNomadEndpoint = errors.New("missing nomad endpoint")
	ErrMissingNomadToken    = errors.New("missing nomad token")
)

func createNomadProvider(config servicediscovery.Config, logger logger.Logger) (servicediscovery.Discoverer, error) {
	nomadEndpoint := config.NomadEndpoint
	if nomadEndpoint == "" {
		return nil, ErrMissingNomadEndpoint
	}

	nomadToken := config.NomadToken
	if nomadToken == "" {
		return nil, ErrMissingNomadToken
	}

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: nomadEndpoint, SecretID: nomadToken})
	if err != nil {
		return nil, fmt.Errorf("creating nomad client: %w", err)
	}

	return servicediscovery.Cached(nomad.NewAllocationsOnPort(client, discovery.FilterTemplateBuildersAndOrchestrators, config.OrchestratorPort), logger), nil
}

var ErrMissingStaticEndpoints = errors.New("missing static endpoints")

func createStaticProvider(config servicediscovery.Config) (servicediscovery.Discoverer, error) {
	static := config.StaticEndpoints
	if len(static) == 0 {
		return nil, ErrMissingStaticEndpoints
	}

	return servicediscovery.NewStatic(static, config.OrchestratorPort), nil
}
