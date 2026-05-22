package discovery

import "context"

// StaticServiceDiscovery returns a fixed (possibly empty) list of items on
// every Query. It is used for local development against the darwin dummy
// orchestrator where no template-builder instances exist.
type StaticServiceDiscovery struct {
	items []Item
}

// NewStaticDiscovery returns a Discovery that always responds with the given
// items. Passing nil yields an empty-but-non-error discovery.
func NewStaticDiscovery(items []Item) Discovery {
	return &StaticServiceDiscovery{items: items}
}

func (sd *StaticServiceDiscovery) Query(_ context.Context) ([]Item, error) {
	if sd.items == nil {
		return []Item{}, nil
	}

	out := make([]Item, len(sd.items))
	copy(out, sd.items)

	return out, nil
}
