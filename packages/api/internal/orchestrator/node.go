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
	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
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
	// Service instance ID is unique identifier for every orchestrator process, after restart it will change.
	// In the future, we want to migrate to using this ID instead of node ID for tracking orchestrators-
	serviceInstanceID string

	commit  string
	version string
}

type nomadServiceDiscovery struct {
	NomadNodeShortID string

	OrchestratorAddress string
	IPAddress           string
}

type Node struct {
	CPUUsage atomic.Int64
	RamUsage atomic.Int64

	// Host metrics
	cpuAllocated         atomic.Int64
	cpuPercent           atomic.Int64
	cpuCount             atomic.Int64
	memoryAllocatedBytes atomic.Int64
	memoryUsedBytes      atomic.Int64
	memoryTotalBytes     atomic.Int64
	sandboxCount         atomic.Int64

	// Detailed disk metrics
	hostDisks      []orchestratorinfo.DiskMetrics
	hostDisksMutex sync.RWMutex

	client   *grpclient.GRPCClient
	clientMd metadata.MD

	Info *node.NodeInfo

	meta   nodeMetadata
	status api.NodeStatus
	mutex  sync.RWMutex

	sbxsInProgress *smap.Map[*sbxInProgress]

	buildCache *ttlcache.Cache[string, interface{}]

	createSuccess atomic.Uint64
	createFails   atomic.Uint64
}

func (n *Node) Close() {
	n.buildCache.Stop()
}

func (n *Node) CloseWithClient() {
	err := n.client.Close()
	if err != nil {
		zap.L().Error("Error closing connection to node", zap.Error(err), logger.WithNodeID(n.Info.NodeID))
	}

	n.Close()
}

func (n *Node) Status() api.NodeStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	if n.status != api.NodeStatusReady {
		return n.status
	}

	switch n.client.Connection.GetState() {
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
		zap.L().Info("Node status changed", logger.WithNodeID(n.Info.NodeID), zap.String("status", string(status)))
		n.status = status
	}
}

func (n *Node) setMetadata(md nodeMetadata) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.meta = md
}

func (n *Node) updateFromServiceInfo(info *orchestratorinfo.ServiceInfoResponse) {
	if info == nil {
		return
	}

	// Update host usage metrics
	n.cpuPercent.Store(info.MetricCpuPercent)
	n.memoryUsedBytes.Store(info.MetricMemoryUsedBytes)

	// Update host total metrics
	n.cpuCount.Store(info.MetricCpuCount)
	n.memoryTotalBytes.Store(info.MetricMemoryTotalBytes)

	// Update total sandbox count
	n.sandboxCount.Store(info.MetricSandboxesRunning)

	// Update detailed disk metrics
	n.updateDisks(info.MetricDisks)
}

func (n *Node) updateDisks(disks []*orchestratorinfo.DiskMetrics) {
	n.hostDisksMutex.Lock()
	defer n.hostDisksMutex.Unlock()
	n.hostDisks = make([]orchestratorinfo.DiskMetrics, len(disks))
	for i, disk := range disks {
		n.hostDisks[i] = orchestratorinfo.DiskMetrics{
			MountPoint:     disk.MountPoint,
			Device:         disk.Device,
			FilesystemType: disk.FilesystemType,
			UsedBytes:      disk.UsedBytes,
			TotalBytes:     disk.TotalBytes,
		}
	}
}

func (n *Node) metadata() nodeMetadata {
	n.mutex.RLock()
	defer n.mutex.RUnlock()
	return n.meta
}

func (n *Node) getHostDisks() []api.DiskMetrics {
	n.hostDisksMutex.RLock()
	defer n.hostDisksMutex.RUnlock()

	result := make([]api.DiskMetrics, len(n.hostDisks))
	for i := range n.hostDisks {
		result[i] = api.DiskMetrics{
			MountPoint:     n.hostDisks[i].MountPoint,
			Device:         n.hostDisks[i].Device,
			FilesystemType: n.hostDisks[i].FilesystemType,
			UsedBytes:      n.hostDisks[i].UsedBytes,
			TotalBytes:     n.hostDisks[i].TotalBytes,
		}
	}
	return result
}

func (n *Node) SendStatusChange(ctx context.Context, s api.NodeStatus) error {
	nodeStatus, ok := ApiNodeToOrchestratorStateMapper[s]
	if !ok {
		zap.L().Error("Unknown service info status", zap.Any("status", s), logger.WithNodeID(n.Info.NodeID))
		return fmt.Errorf("unknown service info status: %s", s)
	}

	client, ctx := n.GetClient(ctx)
	_, err := client.Info.ServiceStatusOverride(ctx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: nodeStatus})
	if err != nil {
		zap.L().Error("Failed to send status change", zap.Error(err))
		return err
	}

	return nil
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]nomadServiceDiscovery, error) {
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

	nodes := make([]nomadServiceDiscovery, 0, len(nomadNodes))
	for _, n := range nomadNodes {
		nodes = append(nodes, nomadServiceDiscovery{
			NomadNodeShortID:    n.ID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%s", n.Address, consts.OrchestratorPort),
			IPAddress:           n.Address,
		})
	}

	return nodes, nil
}

func (o *Orchestrator) GetNode(clusterID uuid.UUID, nodeID string) *Node {
	scopedKey := o.scopedNodeID(clusterID, nodeID)
	n, _ := o.nodes.Get(scopedKey)
	return n
}

func (o *Orchestrator) GetNodeByNomadShortID(id string) *Node {
	for _, n := range o.nodes.Items() {
		if n.Info.NomadNodeShortID == id {
			return n
		}

		metadata := n.metadata()
		nodes[key] = &api.Node{
			NodeID:               key,
			ClusterID:            clusterID,
			Status:               n.Status(),
			CreateSuccesses:      n.createSuccess.Load(),
			CreateFails:          n.createFails.Load(),
			SandboxStartingCount: n.sbxsInProgress.Count(),
			Version:              metadata.version,
			Commit:               metadata.commit,
			AllocatedCPU:         int32(n.cpuAllocated.Load()),
			CpuPercent:           int32(n.cpuPercent.Load()),
			CpuCount:             int32(n.cpuCount.Load()),
			MemoryUsedBytes:      int32(n.memoryUsedBytes.Load()),
			AllocatedMemoryBytes: int32(n.memoryAllocatedBytes.Load()),
			MemoryTotalBytes:     int32(n.memoryTotalBytes.Load()),
			SandboxCount:         int32(n.sandboxCount.Load()),
			HostDisks:            n.getHostDisks(),
		}
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
				NodeID:          key,
				ClusterID:       clusterID,
				Status:          n.Status(),
				CachedBuilds:    builds,
				CreateSuccesses: n.createSuccess.Load(),
				CreateFails:     n.createFails.Load(),
				Version:         metadata.version,
				Commit:          metadata.commit,
			}
		}
	}

	if node == nil {
		return nil
	}

func (o *Orchestrator) NodeCount() int {
	return o.nodes.Count()
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

func (n *Node) GetClient(ctx context.Context) (*grpclient.GRPCClient, context.Context) {
	return n.client, metadata.NewOutgoingContext(ctx, n.clientMd)
}

func (n *Node) GetSandboxCreateCtx(ctx context.Context, req *orchestrator.SandboxCreateRequest) context.Context {
	// Skip local cluster. It should be okay to send it here, but we don't want to do it until we explicitly support it.
	if n.Info.ClusterID == uuid.Nil {
		return metadata.NewOutgoingContext(ctx, n.clientMd)
	}

	md := edge.SerializeSandboxCatalogCreateEvent(
		edge.SandboxCatalogCreateEvent{
			SandboxID:               req.Sandbox.SandboxId,
			SandboxMaxLengthInHours: req.Sandbox.MaxSandboxLength,
			SandboxStartTime:        req.StartTime.AsTime(),

			ExecutionID:    req.Sandbox.ExecutionId,
			OrchestratorID: n.metadata().serviceInstanceID,
		},
	)

	return metadata.NewOutgoingContext(ctx, metadata.Join(n.clientMd, md))
}

func (n *Node) GetSandboxDeleteCtx(ctx context.Context, sandboxID string, executionID string) context.Context {
	// Skip local cluster. It should be okay to send it here, but we don't want to do it until we explicitly support it.
	if n.Info.ClusterID == uuid.Nil {
		return metadata.NewOutgoingContext(ctx, n.clientMd)
	}

	md := edge.SerializeSandboxCatalogDeleteEvent(
		edge.SandboxCatalogDeleteEvent{
			SandboxID:   sandboxID,
			ExecutionID: executionID,
		},
	)

	return metadata.NewOutgoingContext(ctx, metadata.Join(n.clientMd, md))
}
