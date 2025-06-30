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
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type EdgePool struct {
	discovery sd.ServiceDiscoveryAdapter

	nodeSelfHost string
	nodes        *smap.Map[*EdgeNode]

	tracer trace.Tracer
	logger *zap.Logger
}

const (
	edgePoolCacheRefreshInterval = 10 * time.Second
)

var ErrEdgeServiceInstanceNotFound = errors.New("edge service instance not found")

func NewEdgePool(ctx context.Context, logger *zap.Logger, discovery sd.ServiceDiscoveryAdapter, tracer trace.Tracer, nodeSelfHost string) *EdgePool {
	pool := &EdgePool{
		discovery: discovery,

		nodeSelfHost: nodeSelfHost,
		nodes:        smap.New[*EdgeNode](),

		logger: logger,
		tracer: tracer,
	}

	// Background synchronization of orchestrators available in pool
	go func() { pool.keepInSync(ctx) }()

	return pool
}

func (p *EdgePool) GetInstances() map[string]*EdgeNode {
	return p.nodes.Items()
}

func (p *EdgePool) GetInstanceByID(instanceID string) (*EdgeNode, error) {
	for _, node := range p.nodes.Items() {
		if node.GetInfo().ServiceInstanceID == instanceID {
			return node, nil
		}
	}

	return nil, ErrEdgeServiceInstanceNotFound
}

func (p *EdgePool) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	p.syncNodes(ctx)

	ticker := time.NewTicker(edgePoolCacheRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Stopping keep-in-sync")
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
			host := fmt.Sprintf("%s:%d", sdNode.NodeIP, sdNode.NodePort)

			// skip self registration
			if host == p.nodeSelfHost {
				return
			}

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
					p.logger.Error("Error connecting to node", zap.String("host", host), zap.Error(err))
				}

				return
			}
		}(sdNode)
	}

	// wait for all connections to finish
	wg.Wait()

	// disconnect nodes that are not in the list anymore
	for _, node := range p.GetInstances() {
		wg.Add(1)
		go func(node *EdgeNode) {
			defer wg.Done()

			found := false

			for _, sdNode := range sdNodes {
				host := fmt.Sprintf("%s:%d", sdNode.NodeIP, sdNode.NodePort)
				if host == node.GetInfo().Host {
					found = true
					break
				}
			}

			// orchestrator is no longer in the list coming from service discovery
			if !found {
				err := p.removeNode(spanCtx, node)
				if err != nil {
					p.logger.Error("Error during edge node removal", zap.Error(err))
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

	host := fmt.Sprintf("%s:%d", node.NodeIP, node.NodePort)
	o, err := NewEdgeNode(ctx, host)
	if err != nil {
		return err
	}

	p.nodes.Insert(o.GetInfo().ServiceInstanceID, o)
	return nil
}

func (p *EdgePool) removeNode(ctx context.Context, node *EdgeNode) error {
	_, childSpan := p.tracer.Start(ctx, "remove-edge-node")
	defer childSpan.End()

	info := node.GetInfo()
	p.logger.Info("Edge node connection is not active anymore, closing.", l.WithClusterNodeID(info.NodeID))

	// stop background sync and close everything
	err := node.Close()
	if err != nil {
		p.logger.Error("Error closing connection to node", zap.Error(err), l.WithClusterNodeID(info.NodeID))
	}

	p.nodes.Remove(info.ServiceInstanceID)
	p.logger.Info("Edge node node connection has been closed.", l.WithClusterNodeID(info.NodeID))
	return nil
}
