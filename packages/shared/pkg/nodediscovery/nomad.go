package nodediscovery

import (
	"context"
	"fmt"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// nomadDiscovery lists registrations of the Nomad-native services (provider =
// "nomad") that every orchestrator jobspec registers, via GET
// /v1/service/<name> per configured name, unioned. This is node-pool- and
// job-agnostic: any allocation that registers one of the services is
// discovered, which keeps discovery correct while orchestrators migrate
// between jobs, node pools, and service names.
//
// The endpoint returns registrations regardless of health-check status; that
// is acceptable because callers health-gate every discovered instance through
// the orchestrator's gRPC ServiceInfo probe after connecting.
type nomadDiscovery struct {
	noSync

	client       *nomadapi.Client
	serviceNames []string
}

// NewNomad creates a Nomad-backed Discovery that enumerates registrations of
// the given Nomad-native service names (e.g. "orchestrator"), unioned and
// deduplicated by node.
func NewNomad(client *nomadapi.Client, serviceNames []string) Discovery {
	return &nomadDiscovery{
		client:       client,
		serviceNames: serviceNames,
	}
}

func (d *nomadDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	ctx, span := tracer.Start(ctx, "list-nomad-orchestrator-service")
	defer span.End()

	opts := (&nomadapi.QueryOptions{}).WithContext(ctx)

	var out []Instance
	seen := make(map[string]struct{})
	for _, serviceName := range d.serviceNames {
		// Fail if ANY service listing fails: silently degrading to a subset of
		// services would make the sync loop treat the missing orchestrators as
		// gone.
		regs, _, err := d.client.Services().Get(serviceName, opts)
		if err != nil {
			return nil, fmt.Errorf("listing nomad service %q: %w", serviceName, err)
		}

		for _, reg := range regs {
			// Skip unroutable registrations so callers never dial an empty host.
			if reg.Address == "" {
				continue
			}

			// At most one orchestrator runs per node (static port + host
			// flock), so duplicate registrations on a node — within a service
			// (e.g. a stopping allocation whose registration has not been
			// reaped yet) or across services — are transient. Collapse them
			// to keep IDs unique; earlier service names win.
			if _, ok := seen[reg.NodeID]; ok {
				continue
			}
			seen[reg.NodeID] = struct{}{}

			// Truncated Nomad node ID, matching what the node-listing
			// implementation produces, so node identity is stable across the
			// discovery-backend switch.
			shortID := reg.NodeID
			if len(shortID) > consts.NodeIDLength {
				shortID = shortID[:consts.NodeIDLength]
			}

			out = append(out, Instance{
				ID:        shortID,
				IPAddress: reg.Address,
				Port:      uint16(reg.Port),
			})
		}
	}

	return out, nil
}
