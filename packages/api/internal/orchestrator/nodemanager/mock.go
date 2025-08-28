package nodemanager

import (
	"context"
	"math/rand"
	"sync/atomic"
	"time"

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

// mockInfoClient implements infogrpc.InfoServiceClient
type mockInfoClient struct {
	infogrpc.InfoServiceClient
}

// mockSandboxClient implements orchestrator.SandboxServiceClient
type mockSandboxClient struct {
	orchestrator.SandboxServiceClient
}

// Create is a mock implementation that always returns success
func (n *mockSandboxClient) Create(_ context.Context, _ *orchestrator.SandboxCreateRequest, _ ...grpc.CallOption) (*orchestrator.SandboxCreateResponse, error) {
	return &orchestrator.SandboxCreateResponse{}, nil
}

// mockTemplateClient implements templatemanager.TemplateServiceClient
type mockTemplateClient struct {
	templatemanager.TemplateServiceClient
}

// mockSandboxClientWithSleep implements orchestrator.SandboxServiceClient with a sleep on Create
type mockSandboxClientWithSleep struct {
	orchestrator.SandboxServiceClient
	baseSandboxCreateTime time.Duration
}

// Create is a mock implementation that always returns success, with a sleep
func (n *mockSandboxClientWithSleep) Create(_ context.Context, _ *orchestrator.SandboxCreateRequest, _ ...grpc.CallOption) (*orchestrator.SandboxCreateResponse, error) {
	if n.baseSandboxCreateTime != 0 {
		time.Sleep(time.Duration(rand.Int63n(2*n.baseSandboxCreateTime.Milliseconds())) * time.Millisecond)
	}

	return &orchestrator.SandboxCreateResponse{}, nil
}

// newMockGRPCClient creates a new mock gRPC client for testing
func newMockGRPCClient() *grpclient.GRPCClient {
	// Create a dummy connection that will never be used
	conn, _ := grpc.NewClient("localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))

	return &grpclient.GRPCClient{
		Info:       &mockInfoClient{},
		Sandbox:    &mockSandboxClient{},
		Template:   &mockTemplateClient{},
		Connection: conn,
	}
}

type TestOptions func(node *TestNode)

func WithSandboxSleepingClient(baseSandboxCreateTime time.Duration) TestOptions {
	return func(node *TestNode) {
		node.client.Sandbox = &mockSandboxClientWithSleep{
			baseSandboxCreateTime: baseSandboxCreateTime,
		}
	}
}

// NewTestNode creates a properly initialized Node for testing purposes
// It uses a mock gRPC client and has simplified Status() method behavior
func NewTestNode(id string, status api.NodeStatus, cpuAllocated int64, cpuCount uint32, options ...TestOptions) *TestNode {
	node := &Node{
		ID:        id,
		ClusterID: uuid.New(),
		client:    newMockGRPCClient(),
		IPAddress: "127.0.0.1",
		status:    status,
		metrics: Metrics{
			CpuUsage:     cpuAllocated,
			CpuAllocated: uint32(cpuAllocated),
			CpuCount:     cpuCount,
		},
		PlacementMetrics: PlacementMetrics{
			sandboxesInProgress: smap.New[SandboxResources](),
			createSuccess:       atomic.Uint64{},
			createFails:         atomic.Uint64{},
		},
	}

	for _, option := range options {
		option(node)
	}

	return node
}
