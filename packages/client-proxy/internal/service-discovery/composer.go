package service_discovery

import "context"

type ServiceDiscoveryComposer struct {
	edgeServiceDiscovery         ServiceDiscoveryAdapter
	orchestratorServiceDiscovery ServiceDiscoveryAdapter
}

// NewServiceDiscoveryComposer todo: maybe here i can already do pool to take version, healthy etc?
func NewServiceDiscoveryComposer(edgeServiceDiscovery ServiceDiscoveryAdapter, orchestratorServiceDiscovery ServiceDiscoveryAdapter) *ServiceDiscoveryComposer {
	return &ServiceDiscoveryComposer{
		edgeServiceDiscovery:         edgeServiceDiscovery,
		orchestratorServiceDiscovery: orchestratorServiceDiscovery,
	}
}

func (s *ServiceDiscoveryComposer) ListEdgeNodes(ctx context.Context) ([]*ServiceDiscoveryItem, error) {
	return s.edgeServiceDiscovery.ListNodes(ctx)
}

func (s *ServiceDiscoveryComposer) ListOrchestratorNodes(ctx context.Context) ([]*ServiceDiscoveryItem, error) {
	return s.orchestratorServiceDiscovery.ListNodes(ctx)
}
