package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
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

// NewStaticFromAddress returns a Discovery holding a single instance reachable
// at addr, which may be "host:port" or just "host" (defaulting to
// consts.OrchestratorAPIPort). It mirrors the orchestrator-side
// orchestrator/discovery.NewLocal.
//
// A local orchestrator can serve the template-builder role as well
// (ORCHESTRATOR_SERVICES=orchestrator,template-manager). Whether it actually
// does is decided by the roles it reports over the Info RPC during instance
// sync, so pointing template-builder discovery at an orchestrator that does
// not run template-manager (e.g. the darwin dummy) is harmless: the instance
// registers with IsBuilder=false and is skipped when picking a builder.
func NewStaticFromAddress(addr string) (Discovery, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Allow plain "host" without a port.
		host = addr
		portStr = strconv.FormatUint(uint64(consts.OrchestratorAPIPort), 10)
	}
	if host == "" {
		return nil, fmt.Errorf("static discovery: empty host in %q", addr)
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("static discovery: invalid port %q: %w", portStr, err)
	}

	return NewStaticDiscovery([]Item{
		{
			UniqueIdentifier: "local",
			NodeID:           "local",
			// Populated during instance sync.
			InstanceID:           "unknown",
			LocalIPAddress:       host,
			LocalInstanceApiPort: uint16(port),
		},
	}), nil
}

func (sd *StaticServiceDiscovery) Query(_ context.Context) ([]Item, error) {
	if sd.items == nil {
		return []Item{}, nil
	}

	out := make([]Item, len(sd.items))
	copy(out, sd.items)

	return out, nil
}
