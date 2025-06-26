package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type ClusterNode struct {
	ID     string // service id (will change on restart)
	NodeID string // machine id

	Version       string
	VersionCommit string

	roles  []infogrpc.ServiceInfoRole
	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
}

const (
	clusterNodesSyncInterval = 15 * time.Second
)

func (c *Cluster) syncBackground() {
	timer := time.NewTicker(clusterNodesSyncInterval)
	defer timer.Stop()

	for {
		select {
		case <-c.ctx.Done():
			zap.L().Info("Cluster nodes sync ended", l.WithClusterID(c.ID))
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(c.ctx, clusterNodesSyncInterval)
			err := c.sync(syncTimeout)
			syncCancel()

			if err != nil {
				zap.L().Error("Failed to sync cluster nodes", zap.Error(err), l.WithClusterID(c.ID))
			}
		}
	}
}

func (c *Cluster) sync(ctx context.Context) error {
	spanCtx, span := c.tracer.Start(ctx, "keep-in-sync-cluster-nodes")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	// fetch cluster nodes with use of service discovery
	res, err := c.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(spanCtx)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes from service discovery: %w", err)
	}

	if res.StatusCode() != http.StatusOK {
		return fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return errors.New("request to get builders returned nil response")
	}

	var wg sync.WaitGroup

	// register newly discovered nodes
	_, spanNewlyDiscovered := c.tracer.Start(spanCtx, "cluster-nodes-sync-newly-discovered")
	for _, sdNode := range *res.JSON200 {
		clusterId := sdNode.Id
		if _, ok := c.nodes.Get(clusterId); ok {
			// node already exists in the pool, skip it
			continue
		}

		zap.L().Info("Adding new node into cluster nodes pool", l.WithClusterID(c.ID), l.WithClusterNodeID(sdNode.Id))
		node := &ClusterNode{
			ID:     sdNode.Id,
			NodeID: sdNode.NodeId,

			// initial values before first sync
			status: infogrpc.ServiceInfoStatus_OrchestratorUnhealthy,
			roles:  make([]infogrpc.ServiceInfoRole, 0),

			Version:       sdNode.Version,
			VersionCommit: sdNode.Commit,

			mutex: sync.RWMutex{},
		}

		c.nodes.Insert(sdNode.Id, node)
		wg.Add(1)
		go func() {
			c.syncNode(ctx, node)
			wg.Done()
		}()
	}

	// wait for all new nodes to be added
	wg.Wait()
	spanNewlyDiscovered.End()

	// remove nodes that are no longer present in the service discovery
	_, spanOutdatedNodes := c.tracer.Start(spanCtx, "cluster-nodes-sync-outdated-nodes")
	for nodeId, node := range c.nodes.Items() {
		found := false
		for _, sdNode := range *res.JSON200 {
			if sdNode.Id == nodeId {
				found = true
				break
			}
		}

		// synchronize node state
		if found {
			wg.Add(1)
			go func() {
				c.syncNode(ctx, node)
				wg.Done()
			}()

			continue
		}

		zap.L().Info("Removing node from cluster nodes pool", l.WithClusterID(c.ID), l.WithClusterNodeID(nodeId))
		c.nodes.Remove(nodeId)
	}

	// wait for all nodes to be synced
	wg.Wait()
	spanOutdatedNodes.End()

	return nil
}

func (c *Cluster) syncNode(ctx context.Context, node *ClusterNode) {
	client, clientMetadata := c.GetGrpcClient(node.ID)

	// we are taking service info directly from the node to avoid timing delays in service discovery
	reqCtx := metadata.NewOutgoingContext(ctx, clientMetadata)
	info, err := client.Info.ServiceInfo(reqCtx, &emptypb.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		zap.L().Error("Failed to get node service info", zap.Error(err), l.WithClusterID(c.ID), l.WithClusterNodeID(node.ID))
		return
	}

	node.mutex.Lock()
	defer node.mutex.Unlock()

	node.status = info.ServiceStatus
	node.roles = info.ServiceRoles
}

func (n *ClusterNode) GetStatus() infogrpc.ServiceInfoStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return n.status
}

func (n *ClusterNode) hasRole(r infogrpc.ServiceInfoRole) bool {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return slices.Contains(n.roles, r)
}

func (n *ClusterNode) IsBuilderNode() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_TemplateManager)
}

func (n *ClusterNode) IsOrchestratorNode() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_Orchestrator)
}
