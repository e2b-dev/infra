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

type EdgePool struct {
	discovery sd.ServiceDiscoveryAdapter

	nodeSelfHost string
	nodes        *smap.Map[*EdgeNode]
	mutex        sync.Mutex

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	edgePoolCacheRefreshInterval = 10 * time.Second
)

var (
	ErrEdgeNodeNotFound = errors.New("edge node not found")
)

func NewEdgePool(ctx context.Context, logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer, nodeSelfHost string) *EdgePool {
	pool := &EdgePool{
		discovery: discovery,

		nodeSelfHost: nodeSelfHost,
		nodes:        smap.New[*EdgeNode](),
		mutex:        sync.Mutex{},

		logger: logger,
		tracer: tracer,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()

	return pool
}

func (p *EdgePool) GetNodes() map[string]*EdgeNode {
	return p.nodes.Items()
}

func (p *EdgePool) GetNode(id string) (*EdgeNode, error) {
	o, ok := p.nodes.Get(id)
	if !ok {
		return nil, ErrEdgeNodeNotFound
	}

	return o, nil
}

func (p *EdgePool) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	p.syncNodes(ctx)

	ticker := time.NewTicker(edgePoolCacheRefreshInterval)
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

func (p *EdgePool) syncNodes(ctx context.Context) {
	ctxTimeout, cancel := context.WithTimeout(ctx, edgePoolCacheRefreshInterval)
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

			var found *EdgeNode = nil

			host := fmt.Sprintf("%s:%d", sdNode.NodeIp, sdNode.NodePort)

			//// skip self registration
			if host == p.nodeSelfHost {
				p.logger.Debug("Skipping self registration", zap.String("host", host))
				return
			}

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
		go func(node *EdgeNode) {
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
					p.logger.Error("Error during edge node sync", zap.Error(err))
				}
			}
		}(node)

	}

	// wait for all node removals to finish
	wg.Wait()
}

func (p *EdgePool) connectNode(ctx context.Context, node *sd.ServiceDiscoveryItem) error {
	ctx, childSpan := p.tracer.Start(ctx, "connect-edge-node")
	defer childSpan.End()

	host := fmt.Sprintf("%s:%d", node.NodeIp, node.NodePort)
	o, err := NewEdgeNode(ctx, host)
	if err != nil {
		return err
	}

	p.nodes.Insert(o.ServiceId, o)

	// call initial node sync
	return p.syncNode(ctx, o, true)
}

func (p *EdgePool) syncNode(ctx context.Context, node *EdgeNode, foundWithDiscovery bool) error {
	ctx, childSpan := p.tracer.Start(ctx, "sync-edge-node")
	defer childSpan.End()

	// close connection with node
	if !foundWithDiscovery {
		p.logger.Info("Edge node connection is not active anymore, closing.", zap.String("node_id", node.ServiceId))

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

		p.logger.Info("Edge node node connection has been closed.", zap.String("node_id", node.ServiceId))
		return nil
	}

	return nil
}
