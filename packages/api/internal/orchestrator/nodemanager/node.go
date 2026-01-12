package nodemanager

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const UnknownNomadNodeShortID = "unknown"

type NomadServiceDiscovery struct {
	NomadNodeShortID string

	OrchestratorAddress string
	IPAddress           string
}

type Node struct {
	// Deprecated
	NomadNodeShortID string

	ID            string
	ClusterID     uuid.UUID
	IPAddress     string
	SandboxDomain *string

	client *clusters.GRPCClient
	status api.NodeStatus

	metrics   Metrics
	metricsMu sync.RWMutex

	machineInfo machineinfo.MachineInfo
	meta        NodeMetadata

	buildCache *ttlcache.Cache[string, any]

	PlacementMetrics PlacementMetrics

	mutex sync.RWMutex
}

func New(
	ctx context.Context,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	discoveredNode NomadServiceDiscovery,
) (*Node, error) {
	client, err := NewClient(tracerProvider, meterProvider, discoveredNode.OrchestratorAddress)
	if err != nil {
		return nil, err
	}

	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("failed to get node service info: %w", err)
	}

	nodeStatus, ok := OrchestratorToApiNodeStateMapper[nodeInfo.GetServiceStatus()]
	if !ok {
		logger.L().Error(ctx, "Unknown service info status", zap.String("status", nodeInfo.GetServiceStatus().String()), logger.WithNodeID(nodeInfo.GetNodeId()))
		nodeStatus = api.NodeStatusUnhealthy
	}

	buildCache := ttlcache.New[string, any]()
	go buildCache.Start()

	nodeMetadata := NodeMetadata{
		ServiceInstanceID: nodeInfo.GetServiceId(),
		Commit:            nodeInfo.GetServiceCommit(),
		Version:           nodeInfo.GetServiceVersion(),
	}

	n := &Node{
		NomadNodeShortID: discoveredNode.NomadNodeShortID,
		ClusterID:        consts.LocalClusterID,
		ID:               nodeInfo.GetNodeId(),
		IPAddress:        discoveredNode.IPAddress,
		SandboxDomain:    nil,

		client: client,
		status: nodeStatus,
		meta:   nodeMetadata,

		buildCache: buildCache,
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[SandboxResources](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},
	}
	n.UpdateMetricsFromServiceInfoResponse(nodeInfo)
	n.setMachineInfo(nodeInfo.GetMachineInfo())

	return n, nil
}

func NewClusterNode(ctx context.Context, client *clusters.GRPCClient, clusterID uuid.UUID, sandboxDomain *string, i *clusters.Instance) (*Node, error) {
	info := i.GetInfo()
	status, ok := OrchestratorToApiNodeStateMapper[info.Status]
	if !ok {
		logger.L().Error(ctx, "Unknown service info status", zap.String("status", info.Status.String()), logger.WithNodeID(i.NodeID))
		status = api.NodeStatusUnhealthy
	}

	buildCache := ttlcache.New[string, any]()
	go buildCache.Start()

	nodeMetadata := NodeMetadata{
		ServiceInstanceID: info.ServiceInstanceID,
		Commit:            info.ServiceVersionCommit,
		Version:           info.ServiceVersion,
	}

	n := &Node{
		NomadNodeShortID: UnknownNomadNodeShortID,
		ClusterID:        clusterID,
		ID:               i.NodeID,
		// We can't connect directly to the node in the cluster
		IPAddress:     "",
		SandboxDomain: sandboxDomain,
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[SandboxResources](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},

		client: client,
		status: status,
		meta:   nodeMetadata,

		buildCache: buildCache,
	}

	nodeClient, ctx := n.GetClient(ctx)
	nodeInfo, err := nodeClient.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		logger.L().Error(ctx, "Failed to get node service info", zap.Error(err), logger.WithNodeID(n.ID))

		return n, nil
	}

	n.UpdateMetricsFromServiceInfoResponse(nodeInfo)
	n.setMachineInfo(nodeInfo.GetMachineInfo())

	return n, nil
}

func (n *Node) Close(ctx context.Context) error {
	if n.IsNomadManaged() {
		logger.L().Info(ctx, "Closing local node", logger.WithNodeID(n.ID))
		if err := n.client.Close(); err != nil {
			logger.L().Error(ctx, "Error closing client to node", zap.Error(err), logger.WithNodeID(n.ID))
		}
	} else {
		logger.L().Info(ctx, "Closing cluster node", logger.WithNodeID(n.ID), logger.WithClusterID(n.ClusterID))
		// We are not closing grpc client, because it is managed by cluster instance
	}
	n.buildCache.Stop()

	return nil
}

// Ensures that GRPC client request context always has the latest service instance ID
func (n *Node) GetClient(ctx context.Context) (*clusters.GRPCClient, context.Context) {
	return n.client, ctx
}

func (n *Node) IsNomadManaged() bool {
	return n.NomadNodeShortID != UnknownNomadNodeShortID
}
