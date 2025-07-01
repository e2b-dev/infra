package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sd "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type OrchestratorsPool struct {
	discovery       sd.ServiceDiscoveryAdapter
	instances       *smap.Map[*OrchestratorNode]
	synchronization *synchronization.Synchronize[sd.ServiceDiscoveryItem, *OrchestratorNode]

	tracer trace.Tracer
	logger *zap.Logger

	metricProvider metric.MeterProvider
	tracerProvider trace.TracerProvider

	close     chan struct{}
	closeOnce sync.Once
}

const (
	orchestratorsPoolInterval        = 10 * time.Second
	orchestratorsPoolRoundTimeout    = 10 * time.Second
	orchestratorsInstanceSyncTimeout = 10 * time.Second

	statusLogInterval = 1 * time.Minute
)

func NewOrchestratorsPool(
	logger *zap.Logger,
	tracer trace.Tracer,
	tracerProvider trace.TracerProvider,
	metricProvider metric.MeterProvider,
	discovery sd.ServiceDiscoveryAdapter,
) *OrchestratorsPool {
	pool := &OrchestratorsPool{
		discovery: discovery,
		instances: smap.New[*OrchestratorNode](),

		tracer: tracerProvider.Tracer("orchestrators-pool"),
		logger: logger,

		metricProvider: metricProvider,
		tracerProvider: tracerProvider,

		close: make(chan struct{}),
	}

	store := &orchestratorInstancesSyncStore{pool: pool}
	pool.synchronization = synchronization.NewSynchronize(tracer, "orchestrator-instances", "Orchestrator instances", store)

	// Background synchronization of orchestrators pool
	go func() { pool.synchronization.Start(orchestratorsPoolInterval, orchestratorsPoolRoundTimeout, true) }()
	go func() { pool.statusLogSync() }()

	return pool
}

func (p *OrchestratorsPool) GetOrchestrators() map[string]*OrchestratorNode {
	return p.instances.Items()
}

func (p *OrchestratorsPool) GetOrchestrator(instanceID string) (node *OrchestratorNode, ok bool) {
	orchestrators := p.GetOrchestrators()
	for _, node = range orchestrators {
		if node.GetInfo().ServiceInstanceID == instanceID {
			return node, true
		}
	}

	return nil, false
}

func (p *OrchestratorsPool) statusLogSync() {
	ticker := time.NewTicker(statusLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.close:
			p.logger.Info("Stopping analytics sync")
			return
		case <-ticker.C:
			orchestrators := len(p.GetOrchestrators())
			if orchestrators > 0 {
				p.logger.Info(fmt.Sprintf("Orchestrator pool: %d nodes currently in pool", orchestrators))
			} else {
				p.logger.Warn("Orchestrator pool: no orchestrators currently in pool")
			}
		}
	}
}

func (p *OrchestratorsPool) Close(ctx context.Context) error {
	p.synchronization.Close()

	// Close all orchestrator instances in the pool
	for _, instance := range p.instances.Items() {
		err := instance.Close()
		if err != nil {
			p.logger.Error("Error closing orchestrator instance", zap.Error(err), l.WithClusterNodeID(instance.GetInfo().NodeID))
		}
	}

	// Close orchestrators status log sync
	p.closeOnce.Do(
		func() { close(p.close) },
	)

	return nil
}

// SynchronizationStore is an interface that defines methods for synchronizing the orchestrator instances inside the pool.
type orchestratorInstancesSyncStore struct {
	pool *OrchestratorsPool
}

func (e *orchestratorInstancesSyncStore) getHost(ip string, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

func (e *orchestratorInstancesSyncStore) SourceList(ctx context.Context) ([]sd.ServiceDiscoveryItem, error) {
	return e.pool.discovery.ListNodes(ctx)
}

func (e *orchestratorInstancesSyncStore) SourceExists(ctx context.Context, s []sd.ServiceDiscoveryItem, p *OrchestratorNode) bool {
	for _, item := range s {
		itemHost := e.getHost(item.NodeIP, item.NodePort)
		if itemHost == p.GetInfo().Host {
			return true
		}
	}

	return false
}

func (e *orchestratorInstancesSyncStore) PoolList(ctx context.Context) []*OrchestratorNode {
	items := make([]*OrchestratorNode, 0)
	for _, item := range e.pool.instances.Items() {
		items = append(items, item)
	}
	return items
}

func (e *orchestratorInstancesSyncStore) PoolExists(ctx context.Context, source sd.ServiceDiscoveryItem) bool {
	host := e.getHost(source.NodeIP, source.NodePort)
	_, found := e.pool.instances.Get(host)
	return found
}

func (e *orchestratorInstancesSyncStore) PoolInsert(ctx context.Context, source sd.ServiceDiscoveryItem) {
	host := e.getHost(source.NodeIP, source.NodePort)
	o, err := NewOrchestrator(e.pool.tracerProvider, e.pool.metricProvider, source.NodeIP, source.NodePort)
	if err != nil {
		zap.L().Error("failed to register new orchestrator instance", zap.String("host", host), zap.Error(err))
		return
	}

	ctx, cancel := context.WithTimeout(ctx, orchestratorsInstanceSyncTimeout)
	defer cancel()

	// Initial synchronization of the orchestrator instance
	// We want to do it separately here so failed init will cause not adding the instance to the pool
	err = o.sync(ctx)
	if err != nil {
		zap.L().Error("Failed to finish initial orchestrator instance sync", zap.Error(err), l.WithClusterNodeID(o.GetInfo().NodeID))
		return
	}

	e.pool.instances.Insert(host, o)
}

func (e *orchestratorInstancesSyncStore) PoolUpdate(ctx context.Context, item *OrchestratorNode) {
	ctx, cancel := context.WithTimeout(ctx, orchestratorsInstanceSyncTimeout)
	defer cancel()

	err := item.sync(ctx)
	if err != nil {
		zap.L().Error("Failed to sync orchestrator instance", zap.Error(err), l.WithClusterNodeID(item.GetInfo().NodeID))
	}
}

func (e *orchestratorInstancesSyncStore) PoolRemove(ctx context.Context, item *OrchestratorNode) {
	info := item.GetInfo()
	zap.L().Info("Orchestrator instance connection is not active anymore, closing.", l.WithClusterNodeID(info.NodeID))

	err := item.Close()
	if err != nil {
		zap.L().Error("Error closing connection to orchestrator instance", zap.Error(err), l.WithClusterNodeID(info.NodeID))
	}

	e.pool.instances.Remove(item.info.Host)
	zap.L().Info("Orchestrator instance connection has been deregistered.", l.WithClusterNodeID(info.NodeID))
}
