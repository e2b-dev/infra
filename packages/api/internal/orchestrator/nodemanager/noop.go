package nodemanager

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// TestNode is an alias for Node used in testing
type TestNode = Node

// noopInfoClient implements infogrpc.InfoServiceClient
type noopInfoClient struct {
	infogrpc.InfoServiceClient
}

// noopSandboxClient implements orchestrator.SandboxServiceClient
type noopSandboxClient struct {
	orchestrator.SandboxServiceClient
}

// Create is a noop implementation that always returns success
func (n *noopSandboxClient) Create(_ context.Context, _ *orchestrator.SandboxCreateRequest, _ ...grpc.CallOption) (*orchestrator.SandboxCreateResponse, error) {
	return &orchestrator.SandboxCreateResponse{}, nil
}

// noopTemplateClient implements templatemanager.TemplateServiceClient
type noopTemplateClient struct {
	templatemanager.TemplateServiceClient
}

// NewNoopGRPCClient creates a new noop gRPC client for testing
func NewNoopGRPCClient() *grpclient.GRPCClient {
	// Create a dummy connection that will never be used
	conn, _ := grpc.NewClient("localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))

	return &grpclient.GRPCClient{
		Info:       &noopInfoClient{},
		Sandbox:    &noopSandboxClient{},
		Template:   &noopTemplateClient{},
		Connection: conn,
	}
}

// NewTestNode creates a properly initialized Node for testing purposes
// It uses a noop gRPC client and has simplified Status() method behavior
func NewTestNode(id string, status api.NodeStatus, cpuUsage int64) *TestNode {
	node := &Node{
		ID:        id,
		ClusterID: uuid.New(),
		client:    NewNoopGRPCClient(),
		IPAddress: "127.0.0.1",
		status:    status,
		metrics:   Metrics{CpuUsage: cpuUsage},
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[SandboxResources](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},
	}

	return node
}
