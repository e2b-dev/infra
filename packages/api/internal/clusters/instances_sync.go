package clusters

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type instancesSyncStore struct {
	clusterID uuid.UUID

	discovery        discovery.Discovery
	instances        *smap.Map[*Instance]
	instanceCreation func(ctx context.Context, item discovery.Item) (*Instance, error)
}

const (
	instancesSyncInterval = 5 * time.Second
	instancesSyncTimeout  = 5 * time.Second
)

func (d instancesSyncStore) SourceList(ctx context.Context) ([]discovery.Item, error) {
	// Disable discovery for local environments
	if env.IsLocal() {
		logger.L().Debug(ctx, "Service discovery is disabled in local environment")

		return []discovery.Item{}, nil
	}

	items, err := d.discovery.Query(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster instances from service discovery: %w", err)
	}

	return items, nil
}

func (d instancesSyncStore) SourceExists(_ context.Context, s []discovery.Item, p *Instance) bool {
	for _, item := range s {
		// With comparing unique identifier that should ensure we are not re-adding same instance again
		if item.UniqueIdentifier == p.UniqueIdentifier {
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

func (d instancesSyncStore) PoolExists(_ context.Context, item discovery.Item) bool {
	_, found := d.instances.Get(item.NodeID)

	return found
}

func (d instancesSyncStore) PoolInsert(ctx context.Context, item discovery.Item) {
	logger.L().Info(ctx, "Adding instance into cluster pool",
		logger.WithClusterID(d.clusterID),
		logger.WithNodeID(item.NodeID),
		logger.WithServiceInstanceID(item.InstanceID),
		zap.String("identifier", item.UniqueIdentifier),
	)

	i, err := d.instanceCreation(ctx, item)
	if err != nil {
		logger.L().Error(ctx, "Failed to create cluster instance during pool insert",
			zap.Error(err),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(item.NodeID),
			logger.WithServiceInstanceID(item.InstanceID),
		)

		return
	}

	err = i.Sync(ctx)
	if err != nil {
		closeErr := i.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "Failed to close cluster instance after sync failure",
				zap.Error(closeErr),
				logger.WithClusterID(d.clusterID),
				logger.WithNodeID(item.NodeID),
				logger.WithServiceInstanceID(item.InstanceID),
			)
		}

		logger.L().Error(ctx, "Failed to sync cluster instance during pool insert",
			zap.Error(err),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(item.NodeID),
			logger.WithServiceInstanceID(item.InstanceID),
		)

		return
	}

	d.instances.Insert(item.NodeID, i)
}

func (d instancesSyncStore) PoolUpdate(ctx context.Context, i *Instance) {
	err := i.Sync(ctx)
	if err != nil {
		logger.L().Error(ctx, "Failed to sync cluster instance during pool update",
			zap.Error(err),
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(i.NodeID),
			logger.WithServiceInstanceID(i.InstanceID),
		)
	}
}

func (d instancesSyncStore) PoolRemove(ctx context.Context, i *Instance) {
	logger.L().Info(ctx, "Removing instance from cluster pool",
		logger.WithClusterID(d.clusterID),
		logger.WithNodeID(i.NodeID),
		logger.WithServiceInstanceID(i.InstanceID),
	)

	err := i.Close()
	if err != nil {
		logger.L().Warn(ctx, "Failed to close cluster instance during",
			logger.WithClusterID(d.clusterID),
			logger.WithNodeID(i.NodeID),
			logger.WithServiceInstanceID(i.InstanceID),
			zap.Error(err),
		)
	}

	d.instances.Remove(i.NodeID)
}
