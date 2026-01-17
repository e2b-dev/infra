package clusters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// Instance sync store handles synchronization of instances in each cluster and checking its state
type instancesSyncStore struct {
	clusterID uuid.UUID

	discovery        discovery.Discovery
	instances        *smap.Map[*Instance]
	instanceCreation func(ctx context.Context, item discovery.Item) (*Instance, error)
}

func (d instancesSyncStore) SourceList(ctx context.Context) ([]discovery.Item, error) {
	items, err := d.discovery.Query(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster instances from service discovery: %w", err)
	}

	return items, nil
}

func (d instancesSyncStore) SourceExists(_ context.Context, s []discovery.Item, p *Instance) bool {
	for _, item := range s {
		// With comparing unique identifier that should ensure we are not re-adding same instance again
		if item.UniqueIdentifier == p.uniqueIdentifier {
			return true
		}
	}

	return false
}

func (d instancesSyncStore) PoolList(_ context.Context) []*Instance {
	mapped := make([]*Instance, 0)
	for _, item := range d.instances.Items() {
		mapped = append(mapped, item)
	}

	return mapped
}

func (d instancesSyncStore) PoolExists(_ context.Context, s discovery.Item) bool {
	_, found := d.instances.Get(s.NodeID)

	return found
}

func (d instancesSyncStore) PoolInsert(ctx context.Context, item discovery.Item) {
	logger.L().Info(ctx, "Adding instance into cluster pool",
		logger.WithClusterID(d.clusterID),
		logger.WithNodeID(item.NodeID),
		logger.WithServiceInstanceID(item.InstanceID),
	)

	// Instant is synced immediately after creation to ensure it's working before adding to the pool.
	instance, err := d.instanceCreation(ctx, item)
	if err != nil {
		logger.L().Error(ctx, "Failed to create cluster instance during pool insert",
			zap.Error(err),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(item.NodeID),
			logger.WithServiceInstanceID(item.InstanceID),
		)

		return
	}

	d.instances.Insert(item.NodeID, instance)
}

func (d instancesSyncStore) PoolUpdate(ctx context.Context, instance *Instance) {
	_ = d.tryToSyncInstance(ctx, instance)
}

func (d instancesSyncStore) PoolRemove(ctx context.Context, instance *Instance) {
	info := instance.GetInfo()
	logger.L().Info(ctx, "Removing instance from cluster pool",
		logger.WithClusterID(d.clusterID),
		logger.WithNodeID(instance.NodeID),
		logger.WithServiceInstanceID(info.ServiceInstanceID),
	)

	// Try to gracefully close the instance
	d.tryToCloseInstance(ctx, instance)

	d.instances.Remove(instance.NodeID)
}

func (d instancesSyncStore) tryToCloseInstance(ctx context.Context, instance *Instance) {
	closeErr := instance.Close()
	if closeErr != nil {
		info := instance.GetInfo()
		logger.L().Error(ctx, "Failed to close cluster instance after sync failure",
			zap.Error(closeErr),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(instance.NodeID),
			logger.WithServiceInstanceID(info.ServiceInstanceID),
		)
	}
}

func (d instancesSyncStore) tryToSyncInstance(ctx context.Context, instance *Instance) bool {
	err := instance.Sync(ctx)
	if err != nil {
		info := instance.GetInfo()
		logger.L().Error(ctx, "Failed to sync cluster instance",
			zap.Error(err),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(instance.NodeID),
			logger.WithServiceInstanceID(info.ServiceInstanceID),
		)

		return false
	}

	return true
}
