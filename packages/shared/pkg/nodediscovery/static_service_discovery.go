package nodediscovery

import (
	"context"
	"fmt"
)

type StaticServiceDiscovery struct {
	noSync

	items []Instance
}

func NewStaticServiceDiscovery(results []string, port uint16) *StaticServiceDiscovery {
	items := make([]Instance, len(results))
	for i, result := range results {
		items[i] = Instance{ID: fmt.Sprintf("%s:%d", result, port), IPAddress: result, Port: port}
	}

	return &StaticServiceDiscovery{items: items}
}

func (s *StaticServiceDiscovery) ListInstances(_ context.Context) ([]Instance, error) {
	return s.items, nil
}
