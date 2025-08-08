package orchestrator

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const nodeHealthCheckTimeout = time.Second * 2

var (
	OrchestratorToApiNodeStateMapper = map[orchestratorinfo.ServiceInfoStatus]api.NodeStatus{
		orchestratorinfo.ServiceInfoStatus_Healthy:   api.NodeStatusReady,
		orchestratorinfo.ServiceInfoStatus_Draining:  api.NodeStatusDraining,
		orchestratorinfo.ServiceInfoStatus_Unhealthy: api.NodeStatusUnhealthy,
	}

	ApiNodeToOrchestratorStateMapper = map[api.NodeStatus]orchestratorinfo.ServiceInfoStatus{
		api.NodeStatusReady:     orchestratorinfo.ServiceInfoStatus_Healthy,
		api.NodeStatusDraining:  orchestratorinfo.ServiceInfoStatus_Draining,
		api.NodeStatusUnhealthy: orchestratorinfo.ServiceInfoStatus_Unhealthy,
	}
)

func NewClient(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, host string) (*grpclient.GRPCClient, error) {
	conn, err := grpc.NewClient(host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	sandboxClient := orchestrator.NewSandboxServiceClient(conn)
	infoClient := orchestratorinfo.NewInfoServiceClient(conn)

	return &grpclient.GRPCClient{Sandbox: sandboxClient, Info: infoClient, Connection: conn}, nil
}

func (o *Orchestrator) connectToNode(ctx context.Context, discovered nomadServiceDiscovery) error {
	ctx, childSpan := o.tracer.Start(ctx, "connect-to-node")
	defer childSpan.End()

	client, err := NewClient(o.tel.TracerProvider, o.tel.MeterProvider, discovered.OrchestratorAddress)
	if err != nil {
		return err
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeStatus := api.NodeStatusUnhealthy
	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to get node service info: %w", err)
	}

	nodeStatus, ok := OrchestratorToApiNodeStateMapper[nodeInfo.ServiceStatus]
	if !ok {
		zap.L().Error("Unknown service info status", zap.Any("status", nodeInfo.ServiceStatus), logger.WithNodeID(nodeInfo.NodeId))
		nodeStatus = api.NodeStatusUnhealthy
	}

	clusterID := uuid.Nil
	orchestratorNode := &Node{
		client:   client,
		clientMd: make(metadata.MD),

		Info: &node.NodeInfo{
			NomadNodeShortID: discovered.NomadNodeShortID,

			ClusterID: clusterID,
			NodeID:    nodeInfo.NodeId,
			IPAddress: discovered.IPAddress,
		},
		meta: nodeMetadata{
			serviceInstanceID: nodeInfo.ServiceId,
			commit:            nodeInfo.ServiceCommit,
			version:           nodeInfo.ServiceVersion,
		},
		buildCache:     buildCache,
		status:         nodeStatus,
		sbxsInProgress: smap.New[*sbxInProgress](),
		createFails:    atomic.Uint64{},
	}
	// Update host metrics from service info
	orchestratorNode.updateFromServiceInfo(nodeInfo)
	o.registerNode(orchestratorNode)
	return nil
}

func (o *Orchestrator) connectToClusterNode(cluster *edge.Cluster, i *edge.ClusterInstance) {
	// this way we don't need to worry about multiple clusters with the same node ID in shared pool
	poolGrpc := cluster.GetGRPC(i.ServiceInstanceID)

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeStatus, ok := OrchestratorToApiNodeStateMapper[i.GetStatus()]
	if !ok {
		zap.L().Error("Unknown service info status", logger.WithNodeID(i.NodeID))
		nodeStatus = api.NodeStatusUnhealthy
	}

	orchestratorNode := &Node{
		client:   poolGrpc.Client,
		clientMd: poolGrpc.Metadata,

		Info: &node.NodeInfo{
			NomadNodeShortID: node.UnknownNomadNodeShortID,

			ClusterID: cluster.ID,
			NodeID:    i.NodeID,
		},

		status: nodeStatus,
		meta: nodeMetadata{
			serviceInstanceID: i.ServiceInstanceID,
			version:           i.ServiceVersion,
			commit:            i.ServiceVersionCommit,
		},

		buildCache:     buildCache,
		sbxsInProgress: smap.New[*sbxInProgress](),
		createFails:    atomic.Uint64{},
	}

	o.registerNode(orchestratorNode)
}

func (o *Orchestrator) registerNode(node *Node) {
	scopedKey := o.scopedNodeID(node.Info.ClusterID, node.Info.NodeID)
	o.nodes.Insert(scopedKey, node)
}

func (o *Orchestrator) deregisterNode(node *Node) {
	scopedKey := o.scopedNodeID(node.Info.ClusterID, node.Info.NodeID)
	o.nodes.Remove(scopedKey)
}

// When prefixed with cluster ID, node is unique in the map containing nodes from multiple clusters
func (o *Orchestrator) scopedNodeID(clusterID uuid.UUID, nodeID string) string {
	if clusterID == uuid.Nil {
		return nodeID
	}

	return fmt.Sprintf("%s-%s", clusterID.String(), nodeID)
}

func (o *Orchestrator) GetClient(ctx context.Context, clusterID uuid.UUID, nodeID string) (*grpclient.GRPCClient, context.Context, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, nil, fmt.Errorf("node '%s' not found in cluster '%s'", nodeID, clusterID)
	}

	client, ctx := n.GetClient(ctx)
	return client, ctx, nil
}
