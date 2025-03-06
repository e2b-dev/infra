package orchestrator

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

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

	n := &Node{
		Client:         client,
		buildCache:     buildCache,
		sbxsInProgress: smap.New[*sbxInProgress](),
		status:         api.NodeStatusReady,
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
