package servicediscovery

import (
	"context"
	"fmt"
)

type staticDiscovery struct {
	noSync

	items []Instance
}

func NewStatic(results []string, port uint16) Discoverer {
	items := make([]Instance, len(results))
	for i, result := range results {
		items[i] = Instance{ID: fmt.Sprintf("%s:%d", result, port), IPAddress: result, Port: port}
	}

	return &staticDiscovery{items: items}
}

func (s *staticDiscovery) ListInstances(_ context.Context) ([]Instance, error) {
	return s.items, nil
}
