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
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	e2bhealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const nodeHealthCheckTimeout = time.Second * 2

type GRPCClient struct {
	Sandbox orchestrator.SandboxServiceClient
	Info    orchestrator.InfoServiceClient

	connection e2bgrpc.ClientConnInterface
}

func NewClient(host string) (*GRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithBlock(), grpc.WithTimeout(time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	sandboxClient := orchestrator.NewSandboxServiceClient(conn)
	infoClient := orchestrator.NewInfoServiceClient(conn)

	return &GRPCClient{Sandbox: sandboxClient, Info: infoClient, connection: conn}, nil
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func (o *Orchestrator) connectToNode(ctx context.Context, node *node.NodeInfo) error {
	ctx, childSpan := o.tracer.Start(ctx, "connect-to-node")
	childSpan.SetAttributes(attribute.String("node.id", node.ID))

	defer childSpan.End()

	client, err := NewClient(node.OrchestratorAddress)
	if err != nil {
		return err
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	ok, err := o.getNodeHealth(node)
	if err != nil {
		zap.L().Error("Failed to get node health, connecting and marking as unhealthy", zap.Error(err))

		o.nodes.Insert(
			node.ID, &Node{
				Client:         client,
				Info:           node,
				buildCache:     buildCache,
				status:         api.NodeStatusUnhealthy,
				version:        "unknown",
				sbxsInProgress: smap.New[*sbxInProgress](),
				createFails:    atomic.Uint64{},
			},
		)

		return err
	}

	if !ok {
		zap.L().Error("Node is not healthy", zap.String("node_id", node.ID))
		return fmt.Errorf("node is not healthy")
	}

	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to get service info: %w", err)
	}

	o.nodes.Insert(
		node.ID, &Node{
			Client:         client,
			Info:           node,
			buildCache:     buildCache,
			status:         o.getNodeStatusConverted(nodeInfo.ServiceStatus),
			version:        nodeInfo.ServiceVersion,
			sbxsInProgress: smap.New[*sbxInProgress](),
			createFails:    atomic.Uint64{},
		},
	)

	return nil
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	n := o.GetNode(nodeID)
	if n == nil {
		return nil, fmt.Errorf("node '%s' not found", nodeID)
	}

	return n.Client, nil
}

func (o *Orchestrator) getNodeStatusConverted(s orchestrator.ServiceInfoStatus) api.NodeStatus {
	switch s {
	case orchestrator.ServiceInfoStatus_OrchestratorHealthy:
		return api.NodeStatusReady
	case orchestrator.ServiceInfoStatus_OrchestratorDraining:
		return api.NodeStatusDraining
	case orchestrator.ServiceInfoStatus_OrchestratorUnhealthy:
		return api.NodeStatusUnhealthy
	default:
		zap.L().Error("Unknown service info status", zap.Any("status", s))
		return api.NodeStatusUnhealthy
	}
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

	return healthResp.Status == e2bhealth.Healthy, nil
}
