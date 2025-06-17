package service_discovery

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

const (
	orchestratorConfigPrefix = "SERVICE_DISCOVERY_ORCHESTRATOR"
	edgeConfigPrefix         = "SERVICE_DISCOVERY_EDGE"

	DnsProviderKey    = "DNS"
	StaticProviderKey = "STATIC"
	Ec2ProviderKey    = "EC2-INSTANCES"
)

func NewServiceDiscoveryProvider(ctx context.Context, edgePort int, orchestratorPort int, logger *zap.Logger) (edges ServiceDiscoveryAdapter, orchestrators ServiceDiscoveryAdapter, err error) {
	edgeNodes, err := resolveServiceDiscoveryConfig(ctx, edgeConfigPrefix, edgePort, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve edge service discovery config: %w", err)
	}

	orchestratorNodes, err := resolveServiceDiscoveryConfig(ctx, orchestratorConfigPrefix, orchestratorPort, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve orchestrator service discovery config: %w", err)
	}

	return edgeNodes, orchestratorNodes, nil
}

func resolveServiceDiscoveryConfig(ctx context.Context, prefix string, port int, logger *zap.Logger) (ServiceDiscoveryAdapter, error) {
	providerEnv := fmt.Sprintf("%s_PROVIDER", prefix)
	provider := os.Getenv(providerEnv)
	if provider == "" {
		return nil, fmt.Errorf("missing %s environment variable", providerEnv)
	}

	switch provider {
	case DnsProviderKey:
		return createDnsProvider(ctx, prefix, port, logger)
	case Ec2ProviderKey:
		return createEc2Provider(ctx, prefix, port, logger)
	case StaticProviderKey:
		return createStaticProvider(prefix, port)
	}

	return nil, fmt.Errorf("unsupported service discovery provider: %s", provider)
}

func createDnsProvider(ctx context.Context, prefix string, port int, logger *zap.Logger) (ServiceDiscoveryAdapter, error) {
	dnsHostsEnv := fmt.Sprintf("%s_DNS_QUERY", prefix)
	dnsHostsRaw := os.Getenv(dnsHostsEnv)
	if dnsHostsRaw == "" {
		return nil, fmt.Errorf("missing %s environment variable", dnsHostsEnv)
	}

	var dnsResolverAddress string
	dnsResolverEnv := fmt.Sprintf("%s_DNS_RESOLVER_ADDRESS", prefix)
	dnsResolverRaw := os.Getenv(dnsResolverEnv)
	if dnsResolverRaw == "" {
		return nil, fmt.Errorf("missing %s environment variable", dnsResolverEnv)
	} else {
		dnsResolverAddress = dnsResolverRaw
	}

	dnsHosts := strings.Split(dnsHostsRaw, ",")
	return NewDnsServiceDiscovery(ctx, logger, dnsHosts, dnsResolverAddress, port), nil
}

func createEc2Provider(ctx context.Context, prefix string, port int, logger *zap.Logger) (ServiceDiscoveryAdapter, error) {
	regionEnv := fmt.Sprintf("%s_EC2_REGION", prefix)
	region := os.Getenv(regionEnv)
	if region == "" {
		return nil, fmt.Errorf("missing %s environment variable", regionEnv)
	}

	tagsEnv := fmt.Sprintf("%s_EC2_TAGS", prefix)
	tagsRaw := os.Getenv(tagsEnv)
	if tagsRaw == "" {
		return nil, fmt.Errorf("missing %s environment variable", tagsEnv)
	}

	tags := strings.Split(tagsRaw, ",")
	return NewAwsEc2ServiceDiscovery(ctx, region, tags, port, logger)
}

func createStaticProvider(prefix string, port int) (ServiceDiscoveryAdapter, error) {
	env := fmt.Sprintf("%s_STATIC", prefix)
	staticRaw := os.Getenv(env)
	if staticRaw == "" {
		return nil, fmt.Errorf("missing %s environment variable", env)
	}

	static := strings.Split(staticRaw, ",")
	return NewStaticServiceDiscovery(static, port), nil
}
