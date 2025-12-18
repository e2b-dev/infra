package discovery

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type LocalServiceDiscovery struct {
	nomad     *nomadapi.Client
	clusterID uuid.UUID
}

func NewLocalDiscovery(clusterID uuid.UUID, nomad *nomadapi.Client) Discovery {
	return &LocalServiceDiscovery{
		nomad:     nomad,
		clusterID: clusterID,
	}
}

func (sd *LocalServiceDiscovery) Query(ctx context.Context) ([]Item, error) {
	_, span := tracer.Start(ctx, "query-local-cluster-nodes", trace.WithAttributes(telemetry.WithClusterID(sd.clusterID)))
	defer span.End()

	alloc, err := discovery.ListOrchestratorAndTemplateBuilderAllocations(ctx, sd.nomad)
	if err != nil {
		span.RecordError(err)

		return nil, fmt.Errorf("failed to list Nomad allocations in service discovery: %w", err)
	}

	result := make([]Item, len(alloc))
	for i, v := range alloc {
		item := Item{
			UniqueIdentifier: v.AllocationID,
			NodeID:           v.NodeID,

			// For local discovery it's not supported here, but it will be fetched during service sync
			InstanceID: "unknown",

			// For now, we assume ports that are used for gRPC api and proxy are static,
			// in future we should be able to take port numbers from Nomad API and map them accordingly here.
			LocalIPAddress:       v.AllocationIP,
			LocalProxyPort:       consts.OrchestratorProxyPort,
			LocalInstanceApiPort: consts.OrchestratorApiPort,
		}

		result[i] = item
	}

	return result, nil
}
