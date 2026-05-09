package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// nomadDiscovery implements Discovery against a local Nomad HTTP agent.
//
// In the Nomad deploy each client node has the orchestrator binary running
// directly (raw_exec) and listening on consts.OrchestratorAPIPort, so every
// "ready" Nomad node in the configured pool is an orchestrator.
type nomadDiscovery struct {
	client   *nomadapi.Client
	nodePool string
}

// NewNomad creates a Nomad-backed Discovery.
func NewNomad(client *nomadapi.Client, nodePool string) Discovery {
	return &nomadDiscovery{
		client:   client,
		nodePool: nodePool,
	}
}

func (d *nomadDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
	ctx, span := tracer.Start(ctx, "list-nomad-nodes")
	defer span.End()

	options := &nomadapi.QueryOptions{
		Filter: fmt.Sprintf("Status == %q and NodePool == %q", "ready", d.nodePool),
	}
	nodes, _, err := d.client.Nodes().List(options.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		shortID := n.ID
		if len(shortID) > consts.NodeIDLength {
			shortID = shortID[:consts.NodeIDLength]
		}
		out = append(out, Node{
			ShortID:             shortID,
			IPAddress:           n.Address,
			OrchestratorAddress: net.JoinHostPort(n.Address, strconv.FormatUint(uint64(consts.OrchestratorAPIPort), 10)),
		})
	}

	return out, nil
}
