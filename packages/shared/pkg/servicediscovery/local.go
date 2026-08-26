package servicediscovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// localDiscovery returns a single statically configured address, for local
// development against the darwin dummy orchestrator where neither Nomad nor
// Kubernetes is available.
type localDiscovery struct {
	noSync

	instance Instance
}

// NewLocal builds a Discoverer that always returns one instance reachable at
// addr. addr may be "host:port" or just "host"; when the port is omitted,
// consts.OrchestratorAPIPort is used.
func NewLocal(addr string) (Discoverer, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Allow plain "host" without a port.
		host = addr
		portStr = strconv.FormatUint(uint64(consts.OrchestratorAPIPort), 10)
	}
	if host == "" {
		return nil, fmt.Errorf("local discovery: empty host in %q", addr)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("local discovery: invalid port %q: %w", portStr, err)
	}

	return &localDiscovery{
		instance: Instance{
			WorkloadID: "local",
			IPAddress:  host,
			Port:       uint16(port),
		},
	}, nil
}

func (d *localDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	_, span := tracer.Start(ctx, "list-local-nodes")
	defer span.End()

	return []Instance{d.instance}, nil
}
