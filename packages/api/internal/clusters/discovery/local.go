package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var testsInstanceHost = env.GetEnv("TESTS_ORCH_INSTANCE_HOST", "localhost")

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
	ctx, span := tracer.Start(ctx, "query-local-cluster-nodes", trace.WithAttributes(telemetry.WithClusterID(sd.clusterID)))
	defer span.End()

	// Static discovery for local environment.
	// Supports comma-separated hosts for multi-node local setups,
	// e.g. TESTS_ORCH_INSTANCE_HOST=localhost,192.168.100.253
	if env.IsLocal() {
		if testsInstanceHost == "" {
			logger.L().Debug(ctx, "Service discovery is disabled in local environment")

			return []Item{}, nil
		}

		hosts := strings.Split(testsInstanceHost, ",")
		items := make([]Item, 0, len(hosts))
		for i, host := range hosts {
			host = strings.TrimSpace(host)
			if host == "" {
				continue
			}
			nodeID := fmt.Sprintf("local-%d", i)
			if i == 0 {
				nodeID = "local"
			}
			items = append(items, Item{
				UniqueIdentifier:     nodeID,
				NodeID:               nodeID,
				InstanceID:           "unknown",
				LocalIPAddress:       host,
				LocalInstanceApiPort: consts.OrchestratorAPIPort,
			})
		}

		return items, nil
	}

	// For now, we want to search only for template builders as local orchestrators are still discovered
	// old way via Nomad discovery directly inside node manager flow. To minimize changes, we keep it this way for now.
	alloc, err := discovery.ListOrchestratorAndTemplateBuilderAllocations(ctx, sd.nomad, discovery.FilterTemplateBuilders)
	if err != nil {
		span.RecordError(err)

		return nil, fmt.Errorf("failed to list Nomad allocations in service discovery: %w", err)
	}

	result := make([]Item, len(alloc))
	for i, v := range alloc {
		result[i] = Item{
			UniqueIdentifier: v.AllocationID,
			NodeID:           v.NodeID,

			// For local discovery it's not supported here, but it will be fetched during service sync
			InstanceID: "unknown",

			// For now, we assume ports that are used for gRPC api and proxy are static,
			// in future we should be able to take port numbers from Nomad API and map them accordingly here.
			LocalIPAddress:       v.AllocationIP,
			LocalInstanceApiPort: consts.OrchestratorAPIPort,
		}
	}

	return result, nil
}
