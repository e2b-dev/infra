package orchestrator

import (
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type GRPCClient struct {
	Sandbox    orchestrator.SandboxServiceClient
	connection e2bgrpc.ClientConnInterface
}

func NewClient(host string) (*GRPCClient, error) {
	conn, err := e2bgrpc.GetConnection(host, false, grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithBlock(), grpc.WithTimeout(time.Second))
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

func (o *Orchestrator) connectToNode(node *nodeInfo) error {
	client, err := NewClient(node.Address)
	if err != nil {
		return err
	}

	buildCache := ttlcache.New[string, interface{}]()
	go buildCache.Start()

	n := &Node{
		ID:             node.ID,
		Client:         client,
		buildCache:     buildCache,
		sbxsInProgress: make(map[string]*sbxInProgress),
		Status:         api.NodeStatusReady,
	}

	o.nodes[n.ID] = n

	return nil
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	node := o.GetNode(nodeID)
	if node == nil {
		return nil, fmt.Errorf("node '%s' not found", nodeID)
	}

	return node.Client, nil
}
