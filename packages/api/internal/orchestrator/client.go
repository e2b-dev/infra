package orchestrator

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/api/internal/node"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type GRPCClient struct {
	Sandbox    orchestrator.SandboxServiceClient
	connection e2bgrpc.ClientConnInterface
}

func NewClient(host string) (*GRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := orchestrator.NewSandboxServiceClient(conn)

	return &GRPCClient{Sandbox: client, connection: conn}, nil
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func (o *Orchestrator) connectToNode(node *node.NodeInfo) error {
	client, err := NewClient(node.OrchestratorAddress)
	if err != nil {
		return err
	}

	n := &Node{
		ID:     node.ID,
		Client: client,
		Info:   node,
	}

	o.nodes[n.ID] = n

	return nil
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	node, err := o.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	return node.Client, nil
}
