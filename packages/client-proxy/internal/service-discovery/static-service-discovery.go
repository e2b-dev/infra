package service_discovery

import "context"

type StaticServiceDiscovery struct {
	items []ServiceDiscoveryItem
}

func NewStaticServiceDiscovery(results []string, port uint16) *StaticServiceDiscovery {
	items := make([]ServiceDiscoveryItem, 0)

	for _, result := range results {
		items = append(
			items, ServiceDiscoveryItem{InstanceIPAddress: result, InstancePort: port},
		)
	}

	return &StaticServiceDiscovery{items: items}
}

func (s StaticServiceDiscovery) ListInstances(_ context.Context) ([]ServiceDiscoveryItem, error) {
	return s.items, nil
}
