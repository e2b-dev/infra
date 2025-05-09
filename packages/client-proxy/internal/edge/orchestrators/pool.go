package orchestrators

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	sd "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Pool struct {
	sd        sd.ServiceDiscoveryAdapter
	sdFetched *time.Time

	nodes *smap.Map[*Orchestrator]
	mutex sync.Mutex

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	cacheRefreshInterval = 10 * time.Second
)

var (
	ErrOrchestratorNotFound = errors.New("orchestrator not found")
)

func NewOrchestratorsPool(ctx context.Context, logger *zap.Logger, sd sd.ServiceDiscoveryAdapter, tracer trace.Tracer) *Pool {
	pool := &Pool{
		sd:        sd,
		sdFetched: nil,

		nodes: smap.New[*Orchestrator](),
		mutex: sync.Mutex{},

		logger: logger,
		tracer: tracer,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()

	return pool
}

func (p *Pool) GetOrchestrators() (map[string]*Orchestrator, error) {
	return p.nodes.Items(), nil
}

func (p *Pool) GetOrchestrator(id string) (*Orchestrator, error) {
	o, ok := p.nodes.Get(id)
	if !ok {
		return nil, ErrOrchestratorNotFound
	}

	// todo: check orchestrator status
	return o, nil
}

func (p *Pool) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	p.syncNodes(ctx)

	ticker := time.NewTicker(cacheRefreshInterval)
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

func (p *Pool) syncNodes(ctx context.Context) {
	ctxTimeout, cancel := context.WithTimeout(ctx, cacheRefreshInterval)
	defer cancel()

	spanCtx, span := p.tracer.Start(ctxTimeout, "pool-keep-in-sync")
	defer span.End()

	// Service discovery targets
	nodes, err := p.sd.ListNodes(spanCtx)
	if err != nil {
		return
	}

	var wg sync.WaitGroup

	// connect newly discovered orchestrators
	for nodeId, node := range nodes {
		// skip not orchestrators
		if node.ServiceType != sd.ServiceTypeOrchestrator {
			continue
		}

		// skip unhealthy nodes (draining are included)
		if node.Status == sd.StatusUnhealthy {
			continue
		}

		// Already exists, skipping
		_, ok := p.nodes.Get(nodeId)
		if ok {
			continue
		}

		// If the node is not in the list, connect to it
		wg.Add(1)
		go func(n *sd.ServiceDiscoveryItem, id string) {
			defer wg.Done()

			err := p.connectNode(spanCtx, n, nodeId)
			if err != nil {
				p.logger.Error("Error connecting to node", zap.Error(err))
			}
		}(node, nodeId)
	}

	// wait for all connections to finish
	wg.Wait()

	// disconnect nodes that are not in the list anymore
	for nodeId, node := range p.nodes.Items() {
		wg.Add(1)
		go func(nodeId string) {
			defer wg.Done()

			// check if node is in incoming one
			sdNode, sdNodeFound := nodes[nodeId]

			if sdNode != nil && sdNode.Status == sd.StatusUnhealthy {
				// force removal of unhealthy node
				sdNodeFound = true
			}

			err := p.syncNode(spanCtx, nodeId, node, sdNode, sdNodeFound)
			if err != nil {
				p.logger.Error("Error during node sync", zap.Error(err))
			}
		}(nodeId)
	}

	// wait for all node removals to finish
	wg.Wait()
}

func (p *Pool) connectNode(ctx context.Context, node *sd.ServiceDiscoveryItem, nodeId string) error {
	ctx, childSpan := p.tracer.Start(ctx, "connect-orchestrator-node")
	defer childSpan.End()

	o, err := NewOrchestrator(ctx, nodeId, node.NodeIp, node.NodePort, node.ServiceVersion, node.Status)
	if err != nil {
		return err
	}

	p.nodes.Insert(nodeId, o)
	return nil
}

func (p *Pool) syncNode(ctx context.Context, nodeId string, node *Orchestrator, sdNode *sd.ServiceDiscoveryItem, foundWithDiscovery bool) error {
	ctx, childSpan := p.tracer.Start(ctx, "sync-orchestrator-node")
	defer childSpan.End()

	// close connection with node
	if !foundWithDiscovery {
		p.logger.Info("Orchestrator node connection is not active anymore, closing.", zap.String("node_id", nodeId))

		err := node.Close()
		if err != nil {
			p.logger.Error("Error closing connection to node", zap.Error(err))
		}

		p.nodes.Remove(nodeId)
		p.logger.Info("Orchestrator node connection has been closed.", zap.String("node_id", nodeId))
		return nil
	}

	// todo: sync running sandboxes and cached builds?
	node.Status = sdNode.Status

	return nil
}
