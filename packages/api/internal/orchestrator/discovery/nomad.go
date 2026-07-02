package discovery

import (
	"context"
	"net"
	"strconv"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// nomadDiscovery implements Discovery against a local Nomad HTTP agent.
//
// It lists registrations of the Nomad-native service (provider = "nomad")
// that every orchestrator jobspec registers, via GET /v1/service/<name>.
// Unlike the previous implementation — which listed Nomad NODES in a
// hardcoded node pool and assumed each one ran an orchestrator — this is
// node-pool- and job-agnostic: any allocation that registers the service is
// discovered, and nodes without a running orchestrator are never dialed. That
// keeps discovery correct while orchestrators migrate between jobs and node
// pools (system job on the "default" pool, service job on the "orchestrator"
// pool, or both at once mid-migration).
//
// The endpoint returns registrations regardless of health-check status; that
// is acceptable because callers health-gate every discovered instance through
// the orchestrator's gRPC ServiceInfo probe after connecting.
type nomadDiscovery struct {
	client      *nomadapi.Client
	serviceName string
}

// NewNomad creates a Nomad-backed Discovery that enumerates registrations of
// the given Nomad-native service name (e.g. "orchestrator").
func NewNomad(client *nomadapi.Client, serviceName string) Discovery {
	return &nomadDiscovery{
		client:      client,
		serviceName: serviceName,
	}
}

func (d *nomadDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
	ctx, span := tracer.Start(ctx, "list-nomad-orchestrator-service")
	defer span.End()

	opts := (&nomadapi.QueryOptions{}).WithContext(ctx)
	regs, _, err := d.client.Services().Get(d.serviceName, opts)
	if err != nil {
		return nil, err
	}

	out := make([]Node, 0, len(regs))
	seen := make(map[string]struct{}, len(regs))
	for _, reg := range regs {
		// Skip unroutable registrations so callers never dial an empty host.
		if reg.Address == "" {
			continue
		}

		// At most one orchestrator runs per node (static port + host flock),
		// so duplicate registrations on a node are transient — e.g. a
		// stopping allocation whose registration has not been reaped yet.
		// Collapse them to keep ShortIDs unique.
		if _, ok := seen[reg.NodeID]; ok {
			continue
		}
		seen[reg.NodeID] = struct{}{}

		// Truncated Nomad node ID, matching what the node-listing
		// implementation produced, so node identity is stable across the
		// discovery-backend switch.
		shortID := reg.NodeID
		if len(shortID) > consts.NodeIDLength {
			shortID = shortID[:consts.NodeIDLength]
		}

		// The registration carries the service's bound port; fall back to the
		// well-known orchestrator port for defense in depth.
		port := reg.Port
		if port <= 0 {
			port = int(consts.OrchestratorAPIPort)
		}

		out = append(out, Node{
			ShortID:             shortID,
			IPAddress:           reg.Address,
			OrchestratorAddress: net.JoinHostPort(reg.Address, strconv.Itoa(port)),
		})
	}

	return out, nil
}
