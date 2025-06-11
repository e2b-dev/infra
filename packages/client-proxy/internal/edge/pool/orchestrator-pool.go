package pool

import (
	"context"
	"errors"
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
	nodes     *smap.Map[*OrchestratorNode]

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	orchestratorsCacheRefreshInterval = 10 * time.Second
)

var (
	ErrOrchestratorNotFound = errors.New("orchestrator not found")
)

func NewOrchestratorsPool(ctx context.Context, logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer) *OrchestratorsPool {
	pool := &OrchestratorsPool{
		discovery: discovery,

		nodes: smap.New[*OrchestratorNode](),

		logger: logger,
		tracer: tracer,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()

	return pool
}

func (p *OrchestratorsPool) GetOrchestrators() map[string]*OrchestratorNode {
	return p.nodes.Items()
}

func (p *OrchestratorsPool) GetOrchestrator(id string) (node *OrchestratorNode, ok bool) {
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

			// already discovered orchestrator, just sync status etc
			err := p.syncNode(spanCtx, found, true)
			if err != nil {
				p.logger.Error("Error syncing orchestrator node", zap.Error(err))
			}
		}(sdNode)
	}

	// wait for all connections to finish
	wg.Wait()

	// disconnect nodes that are not in the list anymore
	for _, node := range p.nodes.Items() {
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
				err := p.syncNode(spanCtx, node, false)
				if err != nil {
					p.logger.Error("Error during node sync", zap.Error(err))
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

	p.nodes.Insert(o.ServiceId, o)

	// call initial node sync
	return p.syncNode(ctx, o, true)
}

func (p *OrchestratorsPool) syncNode(ctx context.Context, node *OrchestratorNode, foundWithDiscovery bool) error {
	ctx, childSpan := p.tracer.Start(ctx, "sync-orchestrator-node")
	defer childSpan.End()

	// close connection with node
	if !foundWithDiscovery {
		p.logger.Info("Orchestrator node node connection is not active anymore, closing.", zap.String("node_id", node.ServiceId))

		err := node.Close()
		if err != nil {
			p.logger.Error("Error closing connection to node", zap.Error(err))
		}

		// stop background sync and close everything
		err = node.Kill()
		if err != nil {
			p.logger.Error("Error closing connection to node", zap.Error(err))
		}

		p.nodes.Remove(node.ServiceId) // remove from pool
		p.logger.Info("Orchestrator node node connection has been closed.", zap.String("node_id", node.ServiceId))
		return nil
	}

	return nil
}
