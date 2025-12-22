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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type OrchestratorsPool struct {
	discovery       sd.ServiceDiscoveryAdapter
	instances       *smap.Map[*Instance]
	synchronization *synchronization.Synchronize[sd.DiscoveredInstance, *Instance]

	logger logger.Logger

	metricProvider metric.MeterProvider
	tracerProvider trace.TracerProvider

	close     chan struct{}
	closeOnce sync.Once
}

const (
	orchestratorsPoolInterval        = 5 * time.Second
	orchestratorsPoolRoundTimeout    = 5 * time.Second
	orchestratorsInstanceSyncTimeout = 5 * time.Second

	statusLogInterval = 1 * time.Minute
)

func NewOrchestratorsPool(
	ctx context.Context,
	l logger.Logger,
	tracerProvider trace.TracerProvider,
	metricProvider metric.MeterProvider,
	discovery sd.ServiceDiscoveryAdapter,
) *OrchestratorsPool {
	pool := &OrchestratorsPool{
		discovery: discovery,
		instances: smap.New[*Instance](),

		logger: l,

		metricProvider: metricProvider,
		tracerProvider: tracerProvider,

		close: make(chan struct{}),
	}

	store := &orchestratorInstancesSyncStore{pool: pool}
	pool.synchronization = synchronization.NewSynchronize("orchestrator-instances", "Orchestrator instances", store)

	// Background synchronization of orchestrators pool
	go func() {
		pool.synchronization.Start(ctx, orchestratorsPoolInterval, orchestratorsPoolRoundTimeout, true)
	}()
	go func() { pool.statusLogSync(ctx) }()

	return pool
}

func (p *OrchestratorsPool) GetOrchestrators() map[string]*Instance {
	return p.instances.Items()
}

func (p *OrchestratorsPool) GetOrchestrator(instanceID string) (i *Instance, ok bool) {
	orchestrators := p.GetOrchestrators()
	for _, i = range orchestrators {
		if i.GetInfo().ServiceInstanceID == instanceID {
			return i, true
		}
	}

	return nil, false
}

func (p *OrchestratorsPool) statusLogSync(ctx context.Context) {
	ticker := time.NewTicker(statusLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.close:
			p.logger.Info(ctx, "Stopping orchestrators pool sync")

			return
		case <-ticker.C:
			orchestrators := len(p.GetOrchestrators())
			if orchestrators > 0 {
				p.logger.Info(ctx, fmt.Sprintf("Orchestrator pool: %d instances currently in pool", orchestrators))
			} else {
				p.logger.Warn(ctx, "Orchestrator pool: no orchestrators currently in pool")
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
			p.logger.Error(ctx, "Error closing orchestrator Instance", zap.Error(err), logger.WithNodeID(instance.GetInfo().NodeID))
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

func (e *orchestratorInstancesSyncStore) getHost(ip string, port uint16) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

func (e *orchestratorInstancesSyncStore) SourceList(ctx context.Context) ([]sd.DiscoveredInstance, error) {
	return e.pool.discovery.ListInstances(ctx)
}

func (e *orchestratorInstancesSyncStore) SourceExists(_ context.Context, s []sd.DiscoveredInstance, p *Instance) bool {
	itself := p.GetInfo()
	for _, item := range s {
		itemHost := e.getHost(item.InstanceIPAddress, item.InstancePort)
		if itemHost == itself.Host {
			return true
		}
	}

	return false
}

func (e *orchestratorInstancesSyncStore) PoolList(_ context.Context) []*Instance {
	items := make([]*Instance, 0)
	for _, item := range e.pool.instances.Items() {
		items = append(items, item)
	}

	return items
}

func (e *orchestratorInstancesSyncStore) PoolExists(_ context.Context, source sd.DiscoveredInstance) bool {
	host := e.getHost(source.InstanceIPAddress, source.InstancePort)
	_, found := e.pool.instances.Get(host)

	return found
}

func (e *orchestratorInstancesSyncStore) PoolInsert(ctx context.Context, source sd.DiscoveredInstance) {
	host := e.getHost(source.InstanceIPAddress, source.InstancePort)
	o, err := newInstance(e.pool.tracerProvider, e.pool.metricProvider, source.InstanceIPAddress, source.InstancePort)
	if err != nil {
		logger.L().Error(ctx, "failed to register new orchestrator Instance", zap.String("host", host), zap.Error(err))

		return
	}

	ctx, cancel := context.WithTimeout(ctx, orchestratorsInstanceSyncTimeout)
	defer cancel()

	// Initial synchronization of the orchestrator Instance
	// We want to do it separately here so failed init will cause not adding the Instance to the pool
	err = o.sync(ctx)
	if err != nil {
		logger.L().Error(ctx, "Failed to finish initial orchestrator Instance sync", zap.Error(err), logger.WithNodeID(o.GetInfo().NodeID))

		return
	}

	e.pool.instances.Insert(host, o)
}

func (e *orchestratorInstancesSyncStore) PoolUpdate(ctx context.Context, item *Instance) {
	ctx, cancel := context.WithTimeout(ctx, orchestratorsInstanceSyncTimeout)
	defer cancel()

	err := item.sync(ctx)
	if err != nil {
		logger.L().Error(ctx, "Failed to sync orchestrator Instance", zap.Error(err), logger.WithNodeID(item.GetInfo().NodeID))
	}
}

func (e *orchestratorInstancesSyncStore) PoolRemove(ctx context.Context, item *Instance) {
	info := item.GetInfo()
	logger.L().Info(ctx, "Orchestrator Instance connection is not active anymore, closing.", logger.WithNodeID(info.NodeID))

	err := item.Close()
	if err != nil {
		logger.L().Error(ctx, "Error closing connection to orchestrator Instance", zap.Error(err), logger.WithNodeID(info.NodeID))
	}

	e.pool.instances.Remove(info.Host)
	logger.L().Info(ctx, "Orchestrator Instance connection has been deregistered.", logger.WithNodeID(info.NodeID))
}
