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
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	e2bhealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const nodeHealthCheckTimeout = time.Second * 2

type GRPCClient struct {
	Sandbox   orchestratorgrpc.SandboxServiceClient
	Info      orchestratorinfogrpc.InfoServiceClient
	Templates tempaltemanagergrpc.TemplateServiceClient

	connection *grpc.ClientConn
}

var (
	OrchestratorToApiNodeStateMapper = map[orchestratorinfogrpc.ServiceInfoStatus]api.NodeStatus{
		orchestratorinfogrpc.ServiceInfoStatus_OrchestratorHealthy:   api.NodeStatusReady,
		orchestratorinfogrpc.ServiceInfoStatus_OrchestratorDraining:  api.NodeStatusDraining,
		orchestratorinfogrpc.ServiceInfoStatus_OrchestratorUnhealthy: api.NodeStatusUnhealthy,
	}

	ApiNodeToOrchestratorStateMapper = map[api.NodeStatus]orchestratorinfogrpc.ServiceInfoStatus{
		api.NodeStatusReady:     orchestratorinfogrpc.ServiceInfoStatus_OrchestratorHealthy,
		api.NodeStatusDraining:  orchestratorinfogrpc.ServiceInfoStatus_OrchestratorDraining,
		api.NodeStatusUnhealthy: orchestratorinfogrpc.ServiceInfoStatus_OrchestratorUnhealthy,
	}
)

func NewClientWithOptions(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, host string, tls bool, options []grpc.DialOption) (*GRPCClient, error) {
	options = append(
		options,
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithMeterProvider(meterProvider),
			),
		),
		grpc.WithBlock(),

		// we are using client for over-internet connections that needs a bit more time to establish
		grpc.WithTimeout(5*time.Second),
	)

	conn, err := e2bgrpc.GetConnection(host, tls, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	infoClient := orchestratorinfogrpc.NewInfoServiceClient(conn)
	sandboxClient := orchestratorgrpc.NewSandboxServiceClient(conn)
	templateClient := tempaltemanagergrpc.NewTemplateServiceClient(conn)

	return &GRPCClient{
		Sandbox:   sandboxClient,
		Info:      infoClient,
		Templates: templateClient,

		connection: conn,
	}, nil
}

func NewClient(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, host string, tls bool) (*GRPCClient, error) {
	var options []grpc.DialOption
	return NewClientWithOptions(tracerProvider, meterProvider, host, tls, options)
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

	client, err := NewClient(o.tel.TracerProvider, o.tel.MeterProvider, node.OrchestratorAddress, false)
	if err != nil {
		return err
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	nodeStatus := api.NodeStatusUnhealthy
	nodeVersion := "unknown"
	nodeCommit := "unknown"

	ok, err := o.getNodeHealth(node)
	if err != nil {
		zap.L().Error("Failed to get node health, connecting and marking as unhealthy", zap.Error(err))
	}

	if !ok {
		zap.L().Error("Node is not healthy", zap.String("node_id", node.ID))
	}

	nodeInfo, err := client.Info.ServiceInfo(ctx, &emptypb.Empty{})
	if err != nil {
		zap.L().Error("Failed to get node info", zap.Error(err))
	} else {
		nodeStatus, ok = OrchestratorToApiNodeStateMapper[nodeInfo.ServiceStatus]
		if !ok {
			zap.L().Error("Unknown service info status", zap.Any("status", nodeInfo.ServiceStatus), zap.String("node_id", node.ID))
			nodeStatus = api.NodeStatusUnhealthy
		}

		nodeVersion = nodeInfo.ServiceVersion
		nodeCommit = nodeInfo.ServiceCommit
	}

	o.nodes.Insert(
		node.ID, &Node{
			Client:         client,
			Info:           node,
			buildCache:     buildCache,
			status:         nodeStatus,
			version:        nodeVersion,
			commit:         nodeCommit,
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
