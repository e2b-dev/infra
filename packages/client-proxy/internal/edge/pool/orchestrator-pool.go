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
)

type OrchestratorsPool struct {
	discovery sd.ServiceDiscoveryAdapter
	nodes     *smap.Map[*OrchestratorNode]

	tracer trace.Tracer
	logger *zap.Logger

	metricProvider metric.MeterProvider
	tracerProvider trace.TracerProvider
}

const (
	orchestratorsCacheRefreshInterval = 10 * time.Second
	statusLogInterval                 = 1 * time.Minute
)

func NewOrchestratorsPool(
	ctx context.Context,
	logger *zap.Logger,
	tracerProvider trace.TracerProvider,
	metricProvider metric.MeterProvider,
	discovery sd.ServiceDiscoveryAdapter,
) *OrchestratorsPool {
	pool := &OrchestratorsPool{
		discovery: discovery,
		nodes:     smap.New[*OrchestratorNode](),

		tracer: tracerProvider.Tracer("orchestrators-pool"),
		logger: logger,

		metricProvider: metricProvider,
		tracerProvider: tracerProvider,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()
	go func() { pool.statusLogSync(ctx) }()

	return pool
}

func (p *OrchestratorsPool) GetOrchestrators() map[string]*OrchestratorNode {
	return p.nodes.Items()
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

func (p *OrchestratorsPool) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	p.syncNodes(ctx)

	ticker := time.NewTicker(orchestratorsCacheRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Stopping orchestrators keep-in-sync")
			return
		case <-ticker.C:
			p.syncNodes(ctx)
		}
	}
}

func (p *OrchestratorsPool) statusLogSync(ctx context.Context) {
	ticker := time.NewTicker(statusLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
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

func (p *OrchestratorsPool) syncNodes(ctx context.Context) {
	ctxTimeout, cancel := context.WithTimeout(ctx, orchestratorsCacheRefreshInterval)
	defer cancel()

	spanCtx, span := p.tracer.Start(ctxTimeout, "pool-keep-in-sync")
	defer span.End()

	// Service discovery targets
	sdNodes, err := p.discovery.ListNodes(spanCtx)
	if err != nil {
		return
	}

	var wg sync.WaitGroup

	// connect / refresh discovered orchestrators
	for _, sdNode := range sdNodes {
		wg.Add(1)
		go func(sdNode sd.ServiceDiscoveryItem) {
			defer wg.Done()

			var found *OrchestratorNode = nil
			host := fmt.Sprintf("%s:%d", sdNode.NodeIP, sdNode.NodePort)
			for _, node := range p.nodes.Items() {
				if host == node.GetInfo().Host {
					found = node
					break
				}
			}

			if found == nil {
				// newly discovered orchestrator
				err := p.connectNode(ctx, sdNode)
				if err != nil {
					p.logger.Error("Error connecting to node", zap.Error(err), zap.String("host", host))
				}

				return
			}
		}(sdNode)
	}

	// wait for all connections to finish
	wg.Wait()

	// disconnect nodes that are not in the list anymore
	for _, node := range p.GetOrchestrators() {
		wg.Add(1)
		go func(node *OrchestratorNode) {
			nodeInfo := node.GetInfo()
			defer wg.Done()

			found := false

			for _, sdNode := range sdNodes {
				host := fmt.Sprintf("%s:%d", sdNode.NodeIP, sdNode.NodePort)
				if host == nodeInfo.Host {
					found = true
					break
				}
			}

			// orchestrator is no longer in the list coming from service discovery
			if !found {
				err := p.removeNode(spanCtx, node)
				if err != nil {
					p.logger.Error("Error during node removal", zap.Error(err), l.WithClusterNodeID(nodeInfo.NodeID))
				}
			}
		}(node)

	}

	// wait for all node removals to finish
	wg.Wait()
}

func (p *OrchestratorsPool) connectNode(ctx context.Context, node sd.ServiceDiscoveryItem) error {
	ctx, childSpan := p.tracer.Start(ctx, "connect-orchestrator-node")
	defer childSpan.End()

	o, err := NewOrchestrator(ctx, p.tracerProvider, p.metricProvider, node.NodeIP, node.NodePort)
	if err != nil {
		return err
	}

	info := o.GetInfo()
	p.nodes.Insert(info.ServiceInstanceID, o)
	return nil
}

func (p *OrchestratorsPool) removeNode(ctx context.Context, node *OrchestratorNode) error {
	_, childSpan := p.tracer.Start(ctx, "remove-orchestrator-node")
	defer childSpan.End()

	info := node.GetInfo()
	p.logger.Info("Orchestrator node node connection is not active anymore, closing.", l.WithClusterNodeID(info.NodeID))

	// stop background sync and close everything
	err := node.Close()
	if err != nil {
		p.logger.Error("Error closing connection to node", zap.Error(err), l.WithClusterNodeID(info.NodeID))
	}

	p.nodes.Remove(info.ServiceInstanceID)
	p.logger.Info("Orchestrator node node connection has been closed.", l.WithClusterNodeID(info.NodeID))
	return nil
}
