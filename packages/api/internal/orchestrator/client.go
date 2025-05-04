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
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	e2bHealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const nodeHealthCheckTimeout = time.Second * 2

type GRPCClient struct {
	Sandbox orchestrator.SandboxServiceClient
	Health  grpc_health_v1.HealthClient

	connection e2bgrpc.ClientConnInterface
}

func NewClient(host string) (*GRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithBlock(), grpc.WithTimeout(time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := orchestrator.NewSandboxServiceClient(conn)
	health := grpc_health_v1.NewHealthClient(conn)

	return &GRPCClient{Sandbox: client, Health: health, connection: conn}, nil
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

	status := api.NodeStatusUnhealthy
	version := "unknown"

	nodeStatus, err := o.getNodeHealth(node)
	if err != nil {
		zap.L().Error("Failed to get node health", zap.Error(err))
	}

	if nodeStatus != nil {
		if nodeStatus.Status == e2bHealth.Healthy {
			status = api.NodeStatusReady
		} else {
			status = api.NodeStatusUnhealthy
		}

		version = nodeStatus.Version
	}

	n := &Node{
		Client:         client,
		buildCache:     buildCache,
		sbxsInProgress: smap.New[*sbxInProgress](),
		status:         status,
		version:        version,
		Info:           node,
		createFails:    atomic.Uint64{},
	}
	o.nodes.Insert(n.Info.ID, n)

	return nil
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	n := o.GetNode(nodeID)
	if n == nil {
		return nil, fmt.Errorf("node '%s' not found", nodeID)
	}

	return n.Client, nil
}

func (o *Orchestrator) getNodeHealth(node *node.NodeInfo) (*e2bHealth.Response, error) {
	resp, err := o.httpClient.Get(fmt.Sprintf("http://%s/health", node.OrchestratorAddress))
	if err != nil {
		return nil, fmt.Errorf("failed to check node health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node is not healthy: %s", resp.Status)
	}

	// Check if the node is healthy
	var healthResp e2bHealth.Response
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	if err != nil {
		return nil, fmt.Errorf("failed to decode health response: %w", err)
	}

	return &healthResp, nil
}
