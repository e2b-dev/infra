package nodes

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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const UnknownNomadNodeShortID = "unknown"

type sbxInProgress struct {
	MiBMemory int64
	CPUs      int64
}

type NomadServiceDiscovery struct {
	NomadNodeShortID string

	OrchestratorAddress string
	IPAddress           string
}

type Node struct {
	NomadNodeShortID string

	ID        string
	ClusterID uuid.UUID
	IPAddress string

	client *grpclient.GRPCClient
	status api.NodeStatus

	metrics   Metrics
	metricsMu sync.RWMutex

	meta NodeMetadata

	buildCache *ttlcache.Cache[string, interface{}]

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

	nodeStatus := api.NodeStatusUnhealthy
	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to get node service info: %w", err)
	}

	nodeStatus, ok := OrchestratorToApiNodeStateMapper[nodeInfo.ServiceStatus]
	if !ok {
		zap.L().Error("Unknown service info status", zap.Any("status", nodeInfo.ServiceStatus), logger.WithNodeID(nodeInfo.NodeId))
		nodeStatus = api.NodeStatusUnhealthy
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeMetadata := NodeMetadata{
		serviceInstanceID: nodeInfo.ServiceId,
		Commit:            nodeInfo.ServiceCommit,
		Version:           nodeInfo.ServiceVersion,
	}

	clusterID := uuid.Nil
	n := &Node{
		NomadNodeShortID: discoveredNode.NomadNodeShortID,
		ClusterID:        clusterID,
		ID:               nodeInfo.NodeId,
		IPAddress:        discoveredNode.IPAddress,

		client: client,
		status: nodeStatus,
		meta:   nodeMetadata,

		buildCache: buildCache,
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[*sbxInProgress](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},
	}
	n.UpdateMetricsFromServiceInfoResponse(nodeInfo)

	return n, nil
}

func NewClusterNode(
	ctx context.Context,
	client *grpclient.GRPCClient,
	clusterID uuid.UUID,
	i *edge.ClusterInstance,
) (*Node, error) {
	nodeStatus, ok := OrchestratorToApiNodeStateMapper[i.GetStatus()]
	if !ok {
		zap.L().Error("Unknown service info status", zap.Any("status", i.GetStatus()), logger.WithNodeID(i.NodeID))
		nodeStatus = api.NodeStatusUnhealthy
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeMetadata := NodeMetadata{
		serviceInstanceID: i.ServiceInstanceID,
		Commit:            i.ServiceVersionCommit,
		Version:           i.ServiceVersion,
	}

	n := &Node{
		NomadNodeShortID: UnknownNomadNodeShortID,
		ClusterID:        clusterID,
		ID:               i.NodeID,
		// We can't connect directly to the node in the cluster
		IPAddress: "",

		client: client,
		status: nodeStatus,
		meta:   nodeMetadata,

		buildCache: buildCache,
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[*sbxInProgress](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},
	}

	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		zap.L().Error("Failed to get node service info", zap.Error(err), logger.WithNodeID(n.ID))
		return n, nil
	}

	n.UpdateMetricsFromServiceInfoResponse(nodeInfo)

	return n, nil
}

func (n *Node) Close() error {
	n.buildCache.Stop()

	return n.client.Close()
}

func (n *Node) CloseWithClient() error {
	err := n.client.Close()
	if err != nil {
		zap.L().Error("Error closing connection to node", zap.Error(err), logger.WithNodeID(n.ID))
	}

	return n.Close()
}

// Ensures that GRPC client request context always has the latest service instance ID
func (n *Node) GetClient(ctx context.Context) (*grpclient.GRPCClient, context.Context) {
	return n.client, metadata.NewOutgoingContext(ctx, n.getClientMetadata())
}
