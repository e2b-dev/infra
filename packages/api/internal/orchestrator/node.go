package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type sbxInProgress struct {
	MiBMemory int64
	CPUs      int64
}

type nodeMetadata struct {
	orchestratorID string
	commit         string
	version        string
}

type Node struct {
	CPUUsage atomic.Int64
	RamUsage atomic.Int64

	Client   *grpclient.GRPCClient
	ClientMd metadata.MD

	ClusterID     uuid.UUID
	ClusterNodeID string

	Info *node.NodeInfo

	meta   nodeMetadata
	status api.NodeStatus
	mutex  sync.RWMutex

	sbxsInProgress *smap.Map[*sbxInProgress]

	buildCache *ttlcache.Cache[string, interface{}]

	createFails atomic.Uint64
}

func (n *Node) Status() api.NodeStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	if n.status != api.NodeStatusReady {
		return n.status
	}

	switch n.Client.Connection.GetState() {
	case connectivity.Shutdown:
		return api.NodeStatusUnhealthy
	case connectivity.TransientFailure:
		return api.NodeStatusConnecting
	case connectivity.Connecting:
		return api.NodeStatusConnecting
	default:
		break
	}

	return n.status
}

func (n *Node) setStatus(status api.NodeStatus) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if n.status != status {
		zap.L().Info("Node status changed", zap.String("node_id", n.Info.ID), zap.String("status", string(status)))
		n.status = status
	}
}

func (n *Node) setMetadata(i *orchestratorinfo.ServiceInfoResponse, nodeID string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	n.meta = getNodeMetadata(i, nodeID)
}

func (n *Node) metadata() nodeMetadata {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return n.meta
}

func (n *Node) SendStatusChange(ctx context.Context, s api.NodeStatus) error {
	nodeStatus, ok := ApiNodeToOrchestratorStateMapper[s]
	if !ok {
		zap.L().Error("Unknown service info status", zap.Any("status", s), zap.String("node_id", n.Info.ID))
		return fmt.Errorf("unknown service info status: %s", s)
	}

	reqCtx := metadata.NewOutgoingContext(ctx, n.ClientMd)
	_, err := n.Client.Info.ServiceStatusOverride(reqCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: nodeStatus})
	if err != nil {
		zap.L().Error("Failed to send status change", zap.Error(err))
		return err
	}

	return nil
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]*node.NodeInfo, error) {
	_, listSpan := o.tracer.Start(ctx, "list-nomad-nodes")
	defer listSpan.End()

	options := &nomadapi.QueryOptions{
		// TODO: Use variable for node pool name ("default")
		Filter: "Status == \"ready\" and NodePool == \"default\"",
	}
	nomadNodes, _, err := o.nomadClient.Nodes().List(options.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	nodes := make([]*node.NodeInfo, 0, len(nomadNodes))
	for _, n := range nomadNodes {
		nodes = append(nodes, &node.NodeInfo{
			ID:                  n.ID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%s", n.Address, consts.OrchestratorPort),
			IPAddress:           n.Address,
		})
	}

	return nodes, nil
}

func (o *Orchestrator) GetNode(nodeID string) *Node {
	n, _ := o.nodes.Get(nodeID)
	return n
}

// clusterNodeID - this way we don't need to worry about multiple clusters with the same node ID in shared pool
func (o *Orchestrator) clusterNodeID(clusterID uuid.UUID, nodeID string) string {
	clusterPrefix := clusterID.String()[0:7]
	return fmt.Sprintf("%s-%s", clusterPrefix, nodeID)
}

func (o *Orchestrator) GetNodes() []*api.Node {
	nodes := make(map[string]*api.Node)
	for key, n := range o.nodes.Items() {
		var clusterID *string
		if n.ClusterID != uuid.Nil {
			clusterIDRaw := n.ClusterID.String()
			clusterID = &clusterIDRaw
		}

		metadata := n.metadata()
		nodes[key] = &api.Node{
			NodeID:               key,
			ClusterID:            clusterID,
			Status:               n.Status(),
			CreateFails:          n.createFails.Load(),
			SandboxStartingCount: n.sbxsInProgress.Count(),
			Version:              metadata.version,
			Commit:               metadata.commit,
		}
	}

	for _, sbx := range o.instanceCache.Items() {
		n, ok := nodes[sbx.Node.ID]
		if !ok {
			zap.L().Error("node for sandbox wasn't found", logger.WithNodeID(sbx.Node.ID), logger.WithSandboxID(sbx.Instance.SandboxID))
			continue
		}

		n.AllocatedCPU += int32(sbx.VCpu)
		n.AllocatedMemoryMiB += int32(sbx.RamMB)
		n.SandboxCount += 1
	}

	var result []*api.Node
	for _, n := range nodes {
		result = append(result, n)
	}

	return result
}

func (o *Orchestrator) GetNodeDetail(nodeID string) *api.NodeDetail {
	var node *api.NodeDetail

	for key, n := range o.nodes.Items() {
		if key == nodeID {
			var clusterID *string
			if n.ClusterID != uuid.Nil {
				clusterIDRaw := n.ClusterID.String()
				clusterID = &clusterIDRaw
			}

			builds := n.buildCache.Keys()
			metadata := n.metadata()
			node = &api.NodeDetail{
				NodeID:       key,
				ClusterID:    clusterID,
				Status:       n.Status(),
				CachedBuilds: builds,
				CreateFails:  n.createFails.Load(),
				Version:      metadata.version,
				Commit:       metadata.commit,
			}
		}
	}

	if node == nil {
		return nil
	}

	for _, sbx := range o.instanceCache.Items() {
		if sbx.Node.ID == nodeID {
			var metadata *api.SandboxMetadata
			if sbx.Metadata != nil {
				meta := api.SandboxMetadata(sbx.Metadata)
				metadata = &meta
			}
			node.Sandboxes = append(node.Sandboxes, api.ListedSandbox{
				Alias:      sbx.Instance.Alias,
				ClientID:   nodeID,
				CpuCount:   api.CPUCount(sbx.VCpu),
				MemoryMB:   api.MemoryMB(sbx.RamMB),
				EndAt:      sbx.GetEndTime(),
				Metadata:   metadata,
				SandboxID:  sbx.Instance.SandboxID,
				StartedAt:  sbx.StartTime,
				TemplateID: sbx.Instance.TemplateID,
			})
		}
	}

	return node
}

func (n *Node) SyncBuilds(builds []*orchestrator.CachedBuildInfo) {
	for _, build := range builds {
		n.buildCache.Set(build.BuildId, struct{}{}, time.Until(build.ExpirationTime.AsTime()))
	}
}

func (n *Node) InsertBuild(buildID string) {
	exists := n.buildCache.Has(buildID)
	if exists {
		return
	}

	// Set the build in the cache for 2 minutes, it should get updated with the correct time from the orchestrator during sync
	n.buildCache.Set(buildID, struct{}{}, 2*time.Minute)
}

func (o *Orchestrator) NodeCount() int {
	return o.nodes.Count()
}

func getNodeMetadata(n *orchestratorinfo.ServiceInfoResponse, orchestratorID string) nodeMetadata {
	if n == nil {
		return nodeMetadata{
			orchestratorID: orchestratorID,
			commit:         "unknown",
			version:        "unknown",
		}
	}

	return nodeMetadata{
		orchestratorID: n.NodeId,
		commit:         n.ServiceCommit,
		version:        n.ServiceVersion,
	}
}
