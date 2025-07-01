package service_discovery

import "context"

type StaticServiceDiscovery struct {
	items []ServiceDiscoveryItem
}

func NewStaticServiceDiscovery(results []string, port int) *StaticServiceDiscovery {
	items := make([]ServiceDiscoveryItem, 0)

	for _, result := range results {
		items = append(
			items, ServiceDiscoveryItem{NodeIP: result, NodePort: port},
		)
	}

	return &StaticServiceDiscovery{items: items}
}

func (s StaticServiceDiscovery) ListNodes(_ context.Context) ([]ServiceDiscoveryItem, error) {
	return s.items, nil
}
