package clusters

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// SynchronizationStore defines methods for synchronizing cluster instances
type instancesSyncStore struct {
	cluster          *Cluster
	instanceCreation func(ctx context.Context, item api.ClusterOrchestratorNode) (*Instance, error)
}

func (d instancesSyncStore) SourceList(ctx context.Context) ([]api.ClusterOrchestratorNode, error) {
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

func (d instancesSyncStore) SourceExists(_ context.Context, s []api.ClusterOrchestratorNode, p *Instance) bool {
	for _, item := range s {
		// With comparing service instance ID we ensure when orchestrator on same node and node ID is still same
		// we will properly clean up old instance and later register as new one
		if item.ServiceInstanceID == p.InstanceID {
			return true
		}
	}

	return false
}

func (d instancesSyncStore) PoolList(_ context.Context) []*Instance {
	mapped := make([]*Instance, 0)
	for _, item := range d.cluster.instances.Items() {
		mapped = append(mapped, item)
	}

	return mapped
}

func (d instancesSyncStore) PoolExists(_ context.Context, s api.ClusterOrchestratorNode) bool {
	_, found := d.cluster.instances.Get(s.NodeID)

	return found
}

func (d instancesSyncStore) PoolInsert(ctx context.Context, item api.ClusterOrchestratorNode) {
	logger.L().Info(ctx, "Adding instance into cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(item.NodeID),
		logger.WithServiceInstanceID(item.ServiceInstanceID),
	)

	instance, err := d.instanceCreation(ctx, item)
	if err != nil {
		logger.L().Error(ctx, "Failed to create cluster instance during pool insert",
			zap.Error(err),
			logger.WithClusterID(d.cluster.ID),
			logger.WithNodeID(item.NodeID),
			logger.WithServiceInstanceID(item.ServiceInstanceID),
		)

		return
	}

	err = instance.Sync(ctx)
	if err != nil {
		closeErr := instance.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "Failed to close cluster instance after sync failure",
				zap.Error(closeErr),
				logger.WithClusterID(d.cluster.ID),
				logger.WithNodeID(instance.NodeID),
				logger.WithServiceInstanceID(instance.InstanceID),
			)
		}

		logger.L().Error(ctx, "Failed to sync cluster instance during pool insert",
			zap.Error(err),
			logger.WithClusterID(d.cluster.ID),
			logger.WithNodeID(instance.NodeID),
			logger.WithServiceInstanceID(instance.InstanceID),
		)

		return
	}

	d.cluster.instances.Insert(item.NodeID, instance)
}

func (d instancesSyncStore) PoolUpdate(ctx context.Context, instance *Instance) {
	err := instance.Sync(ctx)
	if err != nil {
		logger.L().Error(ctx, "Failed to sync cluster instance during pool update",
			zap.Error(err),
			logger.WithClusterID(d.cluster.ID),
			logger.WithNodeID(instance.NodeID),
			logger.WithServiceInstanceID(instance.InstanceID),
		)
	}
}

func (d instancesSyncStore) PoolRemove(ctx context.Context, instance *Instance) {
	logger.L().Info(ctx, "Removing instance from cluster pool",
		logger.WithClusterID(d.cluster.ID),
		logger.WithNodeID(instance.NodeID),
		logger.WithServiceInstanceID(instance.InstanceID),
	)
	d.cluster.instances.Remove(instance.NodeID)
}
