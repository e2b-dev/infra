package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
)

type ClusterNode struct {
	ID     string // service id (will change on restart)
	NodeID string // machine id

	Version       string
	VersionCommit string

	roles  []infogrpc.ServiceInfoRole
	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
	tracer trace.Tracer
}

const (
	clusterNodesSyncInterval = 15 * time.Second
	clusterNodesSyncTimeout  = 15 * time.Second
)

func (c *Cluster) syncBackground() {
	synchronize := synchronization.Synchronize[api.ClusterOrchestratorNode, string, *ClusterNode]{
		Tracer:           c.tracer,
		TracerSpanPrefix: "cluster-nodes",
		LogsPrefix:       "Cluster nodes",
		Store:            clusterSynchronizationStore{cluster: c},
	}

	synchronize.SyncInBackground(c.close, clusterNodesSyncInterval, clusterNodesSyncTimeout, true)
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

// SynchronizationStore defines methods for synchronizing cluster nodes
type clusterSynchronizationStore struct {
	cluster *Cluster
}

func (d clusterSynchronizationStore) SourceList(ctx context.Context) ([]api.ClusterOrchestratorNode, error) {
	// fetch cluster nodes with use of service discovery
	res, err := d.cluster.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster nodes from service discovery: %w", err)
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get builders returned nil response")
	}

	return *res.JSON200, nil
}

func (d clusterSynchronizationStore) SourceKey(item api.ClusterOrchestratorNode) string {
	return item.Id
}

func (d clusterSynchronizationStore) PoolList(ctx context.Context) map[string]*ClusterNode {
	return d.cluster.nodes.Items()
}

func (d clusterSynchronizationStore) PoolExists(ctx context.Context, key string) bool {
	_, found := d.cluster.nodes.Get(key)
	return found
}

func (d clusterSynchronizationStore) PoolInsert(ctx context.Context, sdNodeID string, sdNode api.ClusterOrchestratorNode) error {
	zap.L().Info("Adding new node into cluster nodes pool", l.WithClusterID(d.cluster.ID), l.WithClusterNodeID(sdNode.Id))
	node := &ClusterNode{
		ID:     sdNode.Id,
		NodeID: sdNode.NodeId,

		// initial values before first sync
		status: infogrpc.ServiceInfoStatus_OrchestratorUnhealthy,
		roles:  make([]infogrpc.ServiceInfoRole, 0),

		Version:       sdNode.Version,
		VersionCommit: sdNode.Commit,

		tracer: d.cluster.tracer,
		mutex:  sync.RWMutex{},
	}

	d.cluster.nodes.Insert(sdNode.Id, node)
	return nil
}

func (d clusterSynchronizationStore) PoolSynchronize(ctx context.Context, nodeID string, node *ClusterNode) {
	d.cluster.syncNode(ctx, node)
}

func (d clusterSynchronizationStore) PoolRemove(ctx context.Context, cluster *ClusterNode) error {
	zap.L().Info("Removing node from cluster nodes pool", l.WithClusterID(d.cluster.ID), l.WithClusterNodeID(cluster.ID))
	d.cluster.nodes.Remove(cluster.ID)
	return nil
}
