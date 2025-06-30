package pool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sd "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type EdgePool struct {
	discovery sd.ServiceDiscoveryAdapter

	instanceSelfHost string
	instances        *smap.Map[*EdgeNode]
	synchronization  *synchronization.Synchronize[*sd.ServiceDiscoveryItem, *EdgeNode]

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	edgeInstancesPoolInterval     = 10 * time.Second
	edgeInstancesPoolRoundTimeout = 10 * time.Second
)

var ErrEdgeServiceInstanceNotFound = errors.New("edge service instance not found")

func NewEdgePool(logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer, instanceSelfHost string) *EdgePool {
	pool := &EdgePool{
		discovery: discovery,

		instanceSelfHost: instanceSelfHost,
		instances:        smap.New[*EdgeNode](),

		logger: logger,
		tracer: tracer,
	}

	store := edgeInstancesSyncStore{pool: pool}
	pool.synchronization = synchronization.NewSynchronize(tracer, "edge-instances", "Edge instances", store)

	// Background synchronization of edge instances available in cluster
	go func() { pool.synchronization.Start(edgeInstancesPoolInterval, edgeInstancesPoolRoundTimeout, true) }()

	return pool
}

func (p *EdgePool) Close() {
	p.synchronization.Close()
}

func (p *EdgePool) GetInstances() map[string]*EdgeNode {
	return p.instances.Items()
}

func (p *EdgePool) GetInstanceByID(instanceID string) (*EdgeNode, error) {
	for _, node := range p.instances.Items() {
		if node.GetInfo().ServiceInstanceID == instanceID {
			return node, nil
		}
	}

	return nil, ErrEdgeServiceInstanceNotFound
}

// SynchronizationStore is an interface that defines methods for synchronizing the edge instances inside the pool.
type edgeInstancesSyncStore struct {
	pool *EdgePool
}

func (e edgeInstancesSyncStore) getHost(ip string, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

func (e edgeInstancesSyncStore) SourceList(ctx context.Context) ([]*sd.ServiceDiscoveryItem, error) {
	return e.pool.discovery.ListNodes(ctx)
}

func (e edgeInstancesSyncStore) SourceExists(ctx context.Context, s []*sd.ServiceDiscoveryItem, p *EdgeNode) bool {
	for _, item := range s {
		itemHost := e.getHost(item.NodeIP, item.NodePort)
		if itemHost == p.GetInfo().Host {
			return true
		}
	}

	return false
}

func (e edgeInstancesSyncStore) PoolList(ctx context.Context) []*EdgeNode {
	items := make([]*EdgeNode, 0)
	for _, item := range e.pool.instances.Items() {
		items = append(items, item)
	}
	return items
}

func (e edgeInstancesSyncStore) PoolExists(ctx context.Context, source *sd.ServiceDiscoveryItem) bool {
	host := e.getHost(source.NodeIP, source.NodePort)
	_, found := e.pool.instances.Get(host)
	return found
}

func (e edgeInstancesSyncStore) PoolInsert(ctx context.Context, source *sd.ServiceDiscoveryItem) {
	host := e.getHost(source.NodeIP, source.NodePort)
	o, err := NewEdgeNode(ctx, host)
	if err != nil {
		zap.L().Error("failed to register new edge instance", zap.String("host", host), zap.Error(err))
		return
	}

	e.pool.instances.Insert(host, o)
}

func (e edgeInstancesSyncStore) PoolUpdate(ctx context.Context, item *EdgeNode) {
	// todo: implement
}

func (e edgeInstancesSyncStore) PoolRemove(ctx context.Context, item *EdgeNode) {
	info := item.GetInfo()
	zap.L().Info("Edge instance connection is not active anymore, closing.", l.WithClusterNodeID(info.NodeID))

	// stop background sync and close everything
	err := item.Close()
	if err != nil {
		zap.L().Error("Error closing connection to instance", zap.Error(err), l.WithClusterNodeID(info.NodeID))
	}

	e.pool.instances.Remove(item.info.Host)
	zap.L().Info("Edge instance connection has been closed.", l.WithClusterNodeID(info.NodeID))
}
