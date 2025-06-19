package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sd "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type OrchestratorsPool struct {
	discovery sd.ServiceDiscoveryAdapter

	nodes *smap.Map[*OrchestratorNode]
	mutex sync.RWMutex

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	orchestratorsCacheRefreshInterval = 10 * time.Second
	statusLogInterval                 = 1 * time.Minute
)

func NewOrchestratorsPool(ctx context.Context, logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer) *OrchestratorsPool {
	pool := &OrchestratorsPool{
		discovery: discovery,

		nodes: smap.New[*OrchestratorNode](),
		mutex: sync.RWMutex{},

		logger: logger,
		tracer: tracer,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()
	go func() { pool.statusLogSync(ctx) }()

	return pool
}

func (p *OrchestratorsPool) GetOrchestrators() map[string]*OrchestratorNode {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.nodes.Items()
}

func (p *OrchestratorsPool) GetOrchestrator(id string) (node *OrchestratorNode, ok bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.nodes.Get(id)
}

func (p *OrchestratorsPool) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	p.syncNodes(ctx)

	ticker := time.NewTicker(orchestratorsCacheRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Stopping keepInSync")
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
		go func(sdNode *sd.ServiceDiscoveryItem) {
			defer wg.Done()

			var found *OrchestratorNode = nil

			host := fmt.Sprintf("%s:%d", sdNode.NodeIp, sdNode.NodePort)
			for _, node := range p.nodes.Items() {
				if host == node.Host {
					found = node
					break
				}
			}

			if found == nil {
				// newly discovered orchestrator
				err := p.connectNode(ctx, sdNode)
				if err != nil {
					p.logger.Error("Error connecting to node", zap.Error(err))
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
			defer wg.Done()

			found := false

			for _, sdNode := range sdNodes {
				host := fmt.Sprintf("%s:%d", sdNode.NodeIp, sdNode.NodePort)
				if host == node.Host {
					found = true
					break
				}
			}

			// orchestrator is no longer in the list coming from service discovery
			if !found {
				err := p.removeNode(spanCtx, node)
				if err != nil {
					p.logger.Error("Error during node removal", zap.Error(err))
				}
			}
		}(node)

	}

	// wait for all node removals to finish
	wg.Wait()
}

func (p *OrchestratorsPool) connectNode(ctx context.Context, node *sd.ServiceDiscoveryItem) error {
	ctx, childSpan := p.tracer.Start(ctx, "connect-orchestrator-node")
	defer childSpan.End()

	o, err := NewOrchestrator(ctx, node.NodeIp, node.NodePort)
	if err != nil {
		return err
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.nodes.Insert(o.ServiceId, o)
	return nil
}

func (p *OrchestratorsPool) removeNode(ctx context.Context, node *OrchestratorNode) error {
	_, childSpan := p.tracer.Start(ctx, "remove-orchestrator-node")
	defer childSpan.End()

	p.logger.Info("Orchestrator node node connection is not active anymore, closing.", zap.String("node_id", node.ServiceId))

	// stop background sync and close everything
	err := node.Close()
	if err != nil {
		p.logger.Error("Error closing connection to node", zap.Error(err))
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.nodes.Remove(node.ServiceId)

	p.logger.Info("Orchestrator node node connection has been closed.", zap.String("node_id", node.ServiceId))
	return nil
}
