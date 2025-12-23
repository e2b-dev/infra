package service_discovery

import "context"

type StaticServiceDiscovery struct {
	items []DiscoveredInstance
}

func NewStaticServiceDiscovery(results []string, port uint16) *StaticServiceDiscovery {
	items := make([]DiscoveredInstance, 0)

	for _, result := range results {
		items = append(
			items, DiscoveredInstance{InstanceIPAddress: result, InstancePort: port},
		)
	}

	return &StaticServiceDiscovery{items: items}
}

func (s StaticServiceDiscovery) ListInstances(_ context.Context) ([]DiscoveredInstance, error) {
	return s.items, nil
}
