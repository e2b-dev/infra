package pool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/authorization"
	sd "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type EdgePool struct {
	discovery sd.ServiceDiscoveryAdapter
	auth      authorization.AuthorizationService

	instanceSelfHost string
	instances        *smap.Map[*EdgeInstance]
	synchronization  *synchronization.Synchronize[sd.ServiceDiscoveryItem, *EdgeInstance]

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	edgeInstancesPoolInterval     = 10 * time.Second
	edgeInstancesPoolRoundTimeout = 10 * time.Second
	edgeInstanceSyncTimeout       = 10 * time.Second
)

var ErrEdgeServiceInstanceNotFound = errors.New("edge service instance not found")

func NewEdgePool(logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer, instanceSelfHost string, auth authorization.AuthorizationService) *EdgePool {
	pool := &EdgePool{
		discovery: discovery,
		auth:      auth,

		instanceSelfHost: instanceSelfHost,
		instances:        smap.New[*EdgeInstance](),

		logger: logger,
		tracer: tracer,
	}

	store := &edgeInstancesSyncStore{pool: pool}
	pool.synchronization = synchronization.NewSynchronize(tracer, "edge-instances", "Edge instances", store)

	// Background synchronization of edge instances available in cluster
	go func() { pool.synchronization.Start(edgeInstancesPoolInterval, edgeInstancesPoolRoundTimeout, true) }()

	return pool
}

func (p *EdgePool) Close(ctx context.Context) error {
	p.synchronization.Close()
	return nil
}

func (p *EdgePool) GetInstances() map[string]*EdgeInstance {
	return p.instances.Items()
}

func (p *EdgePool) GetInstanceByID(instanceID string) (*EdgeInstance, error) {
	for _, i := range p.instances.Items() {
		if i.GetInfo().ServiceInstanceID == instanceID {
			return i, nil
		}
	}

	return nil, ErrEdgeServiceInstanceNotFound
}

// SynchronizationStore is an interface that defines methods for synchronizing the edge instances inside the pool.
type edgeInstancesSyncStore struct {
	pool *EdgePool
}

func (e *edgeInstancesSyncStore) getHost(ip string, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

func (e *edgeInstancesSyncStore) SourceList(ctx context.Context) ([]sd.ServiceDiscoveryItem, error) {
	return e.pool.discovery.ListNodes(ctx)
}

func (e *edgeInstancesSyncStore) SourceExists(ctx context.Context, s []sd.ServiceDiscoveryItem, p *EdgeInstance) bool {
	for _, item := range s {
		itemHost := e.getHost(item.NodeIP, item.NodePort)
		if itemHost == p.GetInfo().Host {
			return true
		}
	}

	return false
}

func (e *edgeInstancesSyncStore) PoolList(ctx context.Context) []*EdgeInstance {
	items := make([]*EdgeInstance, 0)
	for _, item := range e.pool.instances.Items() {
		items = append(items, item)
	}
	return items
}

func (e *edgeInstancesSyncStore) PoolExists(ctx context.Context, source sd.ServiceDiscoveryItem) bool {
	host := e.getHost(source.NodeIP, source.NodePort)
	_, found := e.pool.instances.Get(host)
	return found
}

func (e *edgeInstancesSyncStore) PoolInsert(ctx context.Context, source sd.ServiceDiscoveryItem) {
	host := e.getHost(source.NodeIP, source.NodePort)

	// skip registering itself
	if e.pool.instanceSelfHost == host {
		return
	}

	o, err := NewEdgeInstance(host, e.pool.auth)
	if err != nil {
		zap.L().Error("failed to register new edge instance", zap.String("host", host), zap.Error(err))
		return
	}

	ctx, cancel := context.WithTimeout(ctx, edgeInstanceSyncTimeout)
	defer cancel()

	// Initial synchronization of the edge instance
	// We want to do it separately here so failed init will cause not adding the instance to the pool
	err = o.sync(ctx)
	if err != nil {
		zap.L().Error("Failed to finish initial edge instance sync", zap.Error(err), l.WithNodeID(o.GetInfo().NodeID))
		return
	}

	e.pool.instances.Insert(host, o)
}

func (e *edgeInstancesSyncStore) PoolUpdate(ctx context.Context, item *EdgeInstance) {
	ctx, cancel := context.WithTimeout(ctx, edgeInstanceSyncTimeout)
	defer cancel()

	err := item.sync(ctx)
	if err != nil {
		zap.L().Error("Failed to sync edge instance", zap.Error(err), l.WithNodeID(item.GetInfo().NodeID))
	}
}

func (e *edgeInstancesSyncStore) PoolRemove(ctx context.Context, item *EdgeInstance) {
	info := item.GetInfo()
	zap.L().Info("Edge instance connection is not active anymore, closing.", l.WithNodeID(info.NodeID))

	e.pool.instances.Remove(item.info.Host)
	zap.L().Info("Edge instance connection has been deregistered.", l.WithNodeID(info.NodeID))
}
