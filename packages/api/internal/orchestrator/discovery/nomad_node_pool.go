package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// nomadNodePoolDiscovery implements Discovery by listing Nomad NODES in a
// node pool via the local Nomad agent (GET /v1/nodes), assuming every "ready"
// node in the pool runs an orchestrator listening on the well-known
// consts.OrchestratorAPIPort. This is the pre-service-discovery
// implementation, re-added verbatim as a MIGRATION FALLBACK: orchestrator
// jobs deployed from jobspecs that predate the service port-label fix
// register their service with an empty Address, so the service-based
// discovery (NewNomad) skips them as unroutable. Unioning in this backend
// (see NewMerged) removes any rollout ordering constraint. Once no legacy
// jobs remain, disable via NOMAD_ORCHESTRATOR_LEGACY_DISCOVERY_ENABLED=false
// and delete this file.
//
// Note on draining: Nomad nodes keep Status == "ready" while they drain, so
// a draining orchestrator whose service registrations were already
// deregistered (PreKill) remains discoverable here for the drain window.
type nomadNodePoolDiscovery struct {
	client   *nomadapi.Client
	nodePool string
}

// NewNomadNodePool creates a Nomad-backed Discovery that lists ready nodes in
// the given node pool. See the nomadNodePoolDiscovery doc for why this exists.
func NewNomadNodePool(client *nomadapi.Client, nodePool string) Discovery {
	return &nomadNodePoolDiscovery{
		client:   client,
		nodePool: nodePool,
	}
}

func (d *nomadNodePoolDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
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
		// Truncated the same way the service-based backend truncates the
		// registration's NodeID, so both backends yield the SAME ShortID for
		// the same node and the union can never create duplicate identities.
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
