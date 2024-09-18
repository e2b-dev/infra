package orchestrator

import (
	"fmt"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type GRPCClient struct {
	Sandbox    orchestrator.SandboxClient
	connection e2bgrpc.ClientConnInterface
}

func NewClient(host string) (*GRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, fmt.Errorf("failed to establish GRPC connection: %w", err)
	}

	client := orchestrator.NewSandboxClient(conn)

	return &GRPCClient{Sandbox: client, connection: conn}, nil
}

func (a *GRPCClient) Close() error {
	err := a.connection.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

func (o *Orchestrator) connectToNode(node *nodeInfo) error {
	client, err := NewClient(node.Address)
	if err != nil {
		return err
	}

	n := &Node{
		ID:     node.ID,
		Client: client,
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
