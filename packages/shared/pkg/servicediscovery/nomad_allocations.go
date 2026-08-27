package servicediscovery

import (
	"context"
	"fmt"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// nomadAllocationDiscovery lists Nomad ALLOCATIONS rather than nodes or service
// registrations, which is the only Nomad view that distinguishes one run of a
// process from the next. Consumers that track service instances — a pool that
// must notice a restart and replace its client — need that; consumers that
// track placement targets want nomadDiscovery instead, whose identity survives
// a restart on the same machine.
type nomadAllocationDiscovery struct {
	noSync

	client *nomadapi.Client
	filter discovery.NomadQueryFilter
	port   uint16
}

// NewNomadAllocations creates a Discoverer over the allocations matching filter.
func NewNomadAllocations(client *nomadapi.Client, filter discovery.NomadQueryFilter) Discoverer {
	return NewNomadAllocationsOnPort(client, filter, consts.OrchestratorAPIPort)
}

// NewNomadAllocationsOnPort is NewNomadAllocations for a consumer that carries
// its own port rather than the well-known one.
func NewNomadAllocationsOnPort(client *nomadapi.Client, filter discovery.NomadQueryFilter, port uint16) Discoverer {
	return &nomadAllocationDiscovery{client: client, filter: filter, port: port}
}

func (d *nomadAllocationDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	ctx, span := tracer.Start(ctx, "list-nomad-allocations")
	defer span.End()

	allocations, err := discovery.ListOrchestratorAndTemplateBuilderAllocations(ctx, d.client, d.filter)
	if err != nil {
		return nil, fmt.Errorf("listing nomad allocations: %w", err)
	}

	out := make([]Instance, 0, len(allocations))
	for _, a := range allocations {
		out = append(out, Instance{
			WorkloadID: a.AllocationID,
			NodeID:     a.NodeID,
			IPAddress:  a.AllocationIP,
			Port:       d.port,
		})
	}

	return out, nil
}
