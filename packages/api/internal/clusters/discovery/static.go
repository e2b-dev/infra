package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

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

func NewStaticAddressDiscovery(addr string) (Discovery, error) {
	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid local template builder address %q: %w", addr, err)
	}
	if host == "" {
		return nil, fmt.Errorf("invalid local template builder address %q: empty host", addr)
	}

	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid local template builder port %q: %w", portString, err)
	}
	if port == 0 {
		return nil, fmt.Errorf("invalid local template builder port %q: port must be non-zero", portString)
	}

	return NewStaticDiscovery([]Item{{
		UniqueIdentifier:     "local",
		NodeID:               "local",
		InstanceID:           "unknown",
		LocalIPAddress:       host,
		LocalInstanceApiPort: uint16(port),
	}}), nil
}

func (sd *StaticServiceDiscovery) Query(_ context.Context) ([]Item, error) {
	if sd.items == nil {
		return []Item{}, nil
	}

	out := make([]Item, len(sd.items))
	copy(out, sd.items)

	return out, nil
}
