package nodemanager

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Node struct {
	ID            string
	ClusterID     uuid.UUID
	SandboxDomain *string

	// These values are usefully only for local cluster nodes.
	// Remote one are using proxy and different endpoint for communication that is entirely handled by gRPC connection.
	LocalIPAddress string
	LocalProxyPort uint16

	PlacementMetrics PlacementMetrics

	connection *clusters.GRPCClient
	status     api.NodeStatus

	metrics   Metrics
	metricsMu sync.RWMutex

	machineInfo machineinfo.MachineInfo
	meta        NodeMetadata

	buildCache *ttlcache.Cache[string, any]
	mutex      sync.RWMutex
}

func NewNode(ctx context.Context, clusterID uuid.UUID, sandboxDomain *string, i *clusters.Instance) (*Node, error) {
	nodeStatus, ok := OrchestratorToApiNodeStateMapper[i.GetStatus()]
	if !ok {
		logger.L().Error(ctx, "Unknown service info status", zap.String("status", i.GetStatus().String()), logger.WithNodeID(i.NodeID))
		nodeStatus = api.NodeStatusUnhealthy
	}

	buildCache := ttlcache.New[string, any]()
	go buildCache.Start()

	nodeMetadata := NodeMetadata{
		ServiceInstanceID: i.InstanceID,
		Commit:            i.ServiceVersionCommit,
		Version:           i.ServiceVersion,
	}

	n := &Node{
		ClusterID: clusterID,
		ID:        i.NodeID,

		LocalIPAddress: i.LocalIPAddress,
		LocalProxyPort: i.LocalProxyPort,
		SandboxDomain:  sandboxDomain,
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[SandboxResources](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},

		connection: i.GetConnection(),
		status:     nodeStatus,
		meta:       nodeMetadata,

		buildCache: buildCache,
	}

	nodeInfo, err := n.GetConnection().Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		logger.L().Error(ctx, "Failed to get node service info", zap.Error(err), logger.WithNodeID(n.ID))

		return n, nil
	}

	n.UpdateMetricsFromServiceInfoResponse(nodeInfo)
	n.setMachineInfo(nodeInfo.GetMachineInfo())

	return n, nil
}

func (n *Node) Close(_ context.Context) error {
	n.buildCache.Stop()

	return nil
}

func (n *Node) GetConnection() *clusters.GRPCClient {
	return n.connection
}

func (n *Node) IsLocal() bool {
	return n.ClusterID == consts.LocalClusterID
}
