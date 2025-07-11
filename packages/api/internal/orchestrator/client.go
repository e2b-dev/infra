package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
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
	e2bhealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const nodeHealthCheckTimeout = time.Second * 2

var (
	OrchestratorToApiNodeStateMapper = map[orchestratorinfo.ServiceInfoStatus]api.NodeStatus{
		orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy:   api.NodeStatusReady,
		orchestratorinfo.ServiceInfoStatus_OrchestratorDraining:  api.NodeStatusDraining,
		orchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy: api.NodeStatusUnhealthy,
	}

	ApiNodeToOrchestratorStateMapper = map[api.NodeStatus]orchestratorinfo.ServiceInfoStatus{
		api.NodeStatusReady:     orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy,
		api.NodeStatusDraining:  orchestratorinfo.ServiceInfoStatus_OrchestratorDraining,
		api.NodeStatusUnhealthy: orchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy,
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

func (o *Orchestrator) connectToNode(ctx context.Context, node *node.NodeInfo) error {
	ctx, childSpan := o.tracer.Start(ctx, "connect-to-node")
	childSpan.SetAttributes(attribute.String("node.id", node.ID))

	defer childSpan.End()

	client, err := NewClient(o.tel.TracerProvider, o.tel.MeterProvider, node.OrchestratorAddress)
	if err != nil {
		return err
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeStatus := api.NodeStatusUnhealthy

	ok, err := o.getNodeHealth(node)
	if err != nil {
		zap.L().Error("Failed to get node health, connecting and marking as unhealthy", zap.Error(err))
	}

	if !ok {
		zap.L().Error("Node is not healthy", logger.WithNodeID(node.ID))
	}

	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		zap.L().Error("Failed to get node info", zap.Error(err))
	} else {
		nodeStatus, ok = OrchestratorToApiNodeStateMapper[nodeInfo.ServiceStatus]
		if !ok {
			zap.L().Error("Unknown service info status", zap.Any("status", nodeInfo.ServiceStatus), logger.WithNodeID(node.ID))
			nodeStatus = api.NodeStatusUnhealthy
		}
	}

	o.nodes.Insert(
		node.ID, &Node{
			client:   client,
			clientMd: make(metadata.MD),

			Info:           node,
			meta:           getNodeMetadata(nodeInfo, node.ID),
			buildCache:     buildCache,
			status:         nodeStatus,
			sbxsInProgress: smap.New[*sbxInProgress](),
			createFails:    atomic.Uint64{},
		},
	)

	return nil
}

func (o *Orchestrator) connectToClusterNode(cluster *edge.Cluster, i *edge.ClusterInstance) {
	// this way we don't need to worry about multiple clusters with the same node ID in shared pool
	poolNodeID := o.clusterNodeID(cluster.ID, i.NodeID)
	poolGrpc := cluster.GetGRPC(i.ServiceInstanceID)

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	orchestratorNode := &Node{
		client:   poolGrpc.Client,
		clientMd: poolGrpc.Metadata,

		ClusterID:     cluster.ID,
		ClusterNodeID: i.NodeID,

		// some places are using this id to get node from orchestrator pool
		// probably we can get rid of this and just create ID directly on Node struct
		Info: &node.NodeInfo{
			ID: poolNodeID,
		},

		status: OrchestratorToApiNodeStateMapper[i.GetStatus()],
		meta: nodeMetadata{
			orchestratorID: poolNodeID,
			version:        i.ServiceVersion,
			commit:         i.ServiceVersionCommit,
		},

		buildCache:     buildCache,
		sbxsInProgress: smap.New[*sbxInProgress](),
		createFails:    atomic.Uint64{},
	}

	o.nodes.Insert(poolNodeID, orchestratorNode)
}

func (o *Orchestrator) GetClient(ctx context.Context, nodeID string) (*grpclient.GRPCClient, context.Context, error) {
	n := o.GetNode(nodeID)
	if n == nil {
		return nil, nil, fmt.Errorf("node '%s' not found", nodeID)
	}

	client, reqCtx := n.GetClient(ctx)
	return client, reqCtx, nil
}

func (o *Orchestrator) getNodeHealth(node *node.NodeInfo) (bool, error) {
	resp, err := o.httpClient.Get(fmt.Sprintf("http://%s/health", node.OrchestratorAddress))
	if err != nil {
		return false, fmt.Errorf("failed to check node health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("node is not healthy: %s", resp.Status)
	}

	// Check if the node is healthy
	var healthResp e2bhealth.Response
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	if err != nil {
		return false, fmt.Errorf("failed to decode health response: %w", err)
	}

	isUsable := healthResp.Status == e2bhealth.Healthy || healthResp.Status == e2bhealth.Draining
	return isUsable, nil
}
