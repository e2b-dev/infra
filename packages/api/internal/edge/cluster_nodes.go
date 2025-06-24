package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ClusterNode struct {
	Id     string // service id (will change on restart)
	NodeId string // machine id

	Status infogrpc.ServiceInfoStatus
	Roles  []infogrpc.ServiceInfoRole

	Version       string
	VersionCommit string

	mutex sync.RWMutex // mutex to protect the node state
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
			zap.L().Info("Cluster nodes sync ended", l.WithClusterID(c.Id))
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(c.ctx, clusterNodesSyncInterval)
			err := c.sync(syncTimeout)
			syncCancel()

			if err != nil {
				zap.L().Error("Failed to sync cluster nodes", zap.Error(err), l.WithClusterID(c.Id))
			}
		}
	}
}

func (c *Cluster) sync(ctx context.Context) error {
	_, span := c.tracer.Start(ctx, "keep-in-sync-cluster-nodes")
	defer span.End()

	// fetch cluster nodes with use of service discovery
	res, err := c.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(c.ctx)
	if err != nil {
		zap.L().Error("Failed to get cluster nodes", zap.Error(err), l.WithClusterID(c.Id))
		return err
	}

	if res.StatusCode() != http.StatusOK {
		return fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return errors.New("request to get builders returned nil response")
	}

	var wg sync.WaitGroup

	// register newly discovered nodes
	for _, sdNode := range *res.JSON200 {
		clusterId := sdNode.Id
		if _, ok := c.nodes.Get(clusterId); ok {
			// node already exists in the pool, skip it
			continue
		}

		zap.L().Info("Adding new node into cluster nodes pool", l.WithClusterID(c.Id), l.WithClusterNodeID(sdNode.Id))
		node := &ClusterNode{
			Id:     sdNode.Id,
			NodeId: sdNode.NodeId,

			// initial values before first sync
			Status: infogrpc.ServiceInfoStatus_OrchestratorDraining,
			Roles:  make([]infogrpc.ServiceInfoRole, 0),

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

	// remove nodes that are no longer present in the service discovery
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

		zap.L().Info("Removing node from cluster nodes pool", l.WithClusterID(c.Id), l.WithClusterNodeID(nodeId))
		c.nodes.Remove(nodeId)
	}

	// wait for all nodes to be synced
	wg.Wait()

	return nil
}

func (c *Cluster) syncNode(ctx context.Context, node *ClusterNode) {
	client, clientMetadata := c.GetGrpcClient(node.Id)

	// we are taking service info directly from the node to avoid timing delays in service discovery
	reqCtx := metadata.NewOutgoingContext(ctx, clientMetadata)
	info, err := client.Info.ServiceInfo(reqCtx, &emptypb.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		zap.L().Error("Failed to get node service info", zap.Error(err), l.WithClusterID(c.Id), l.WithClusterNodeID(node.Id))
		return
	}

	node.mutex.Lock()
	defer node.mutex.Unlock()

	node.Status = info.ServiceStatus
	node.Roles = info.ServiceRoles
}
