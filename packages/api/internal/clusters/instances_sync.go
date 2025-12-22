package clusters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// SynchronizationStore defines methods for synchronizing cluster instances
type clusterSynchronizationStore struct {
	cluster *Cluster
}

func (d clusterSynchronizationStore) SourceList(ctx context.Context) ([]api.ClusterOrchestratorNode, error) {
	// fetch cluster instances with use of service discovery
	res, err := d.cluster.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster instances from service discovery: %w", err)
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get builders returned nil response")
	}

	return *res.JSON200, nil
}

func (d clusterSynchronizationStore) SourceExists(_ context.Context, s []api.ClusterOrchestratorNode, p *Instance) bool {
	for _, item := range s {
		// With comparing service instance ID we ensure when orchestrator on same node and node ID is still same
		// we will properly clean up old instance and later register as new one
		if item.ServiceInstanceID == p.InstanceID {
			return true
		}
	}

	return false
}

func (d clusterSynchronizationStore) PoolList(_ context.Context) []*Instance {
	mapped := make([]*Instance, 0)
	for _, item := range d.cluster.instances.Items() {
		mapped = append(mapped, item)
	}

	return mapped
}

func (d clusterSynchronizationStore) PoolExists(_ context.Context, s api.ClusterOrchestratorNode) bool {
	_, found := d.cluster.instances.Get(s.NodeID)

	return found
}

func (d clusterSynchronizationStore) PoolInsert(ctx context.Context, item api.ClusterOrchestratorNode) {
	logger.L().Info(ctx, "Adding instance into cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(item.NodeID),
		logger.WithServiceInstanceID(item.ServiceInstanceID),
	)

	instance := &Instance{
		NodeID: item.NodeID,

		InstanceID:           item.ServiceInstanceID,
		ServiceVersion:       item.ServiceVersion,
		ServiceVersionCommit: item.ServiceVersionCommit,

		// initial values before first sync
		status: infogrpc.ServiceInfoStatus_Unhealthy,
		roles:  make([]infogrpc.ServiceInfoRole, 0),

		mutex: sync.RWMutex{},
	}

	d.cluster.syncInstance(ctx, instance)
	d.cluster.instances.Insert(item.NodeID, instance)
}

func (d clusterSynchronizationStore) PoolUpdate(ctx context.Context, instance *Instance) {
	d.cluster.syncInstance(ctx, instance)
}

func (d clusterSynchronizationStore) PoolRemove(ctx context.Context, instance *Instance) {
	logger.L().Info(ctx, "Removing instance from cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(instance.NodeID),
		logger.WithServiceInstanceID(instance.InstanceID),
	)
	d.cluster.instances.Remove(instance.NodeID)
}
