package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// localDiscovery returns a single statically configured orchestrator address.
//
// It is meant for local development against the darwin dummy orchestrator
// (packages/orchestrator/main_darwin.go) where neither Nomad nor Kubernetes is
// available. The single returned Node is fixed at construction time.
type localDiscovery struct {
	node Node
}

// NewLocal builds a Discovery that always returns one orchestrator instance
// reachable at addr. addr may be "host:port" or just "host"; when the port is
// omitted, consts.OrchestratorAPIPort is used.
func NewLocal(addr string) (Discovery, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Allow plain "host" without a port.
		host = addr
		portStr = strconv.FormatUint(uint64(consts.OrchestratorAPIPort), 10)
	}
	if host == "" {
		return nil, fmt.Errorf("local discovery: empty host in %q", addr)
	}
	if _, err := strconv.ParseUint(portStr, 10, 16); err != nil {
		return nil, fmt.Errorf("local discovery: invalid port %q: %w", portStr, err)
	}

	return &localDiscovery{
		node: Node{
			ShortID:             "local",
			IPAddress:           host,
			OrchestratorAddress: net.JoinHostPort(host, portStr),
		},
	}, nil
}

func (d *localDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
	_, span := tracer.Start(ctx, "list-local-nodes")
	defer span.End()

	return []Node{d.node}, nil
}
