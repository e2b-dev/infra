package placement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type mockAlgorithm struct {
	mock.Mock
}

func (m *mockAlgorithm) chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources) (*nodemanager.Node, error) {
	args := m.Called(ctx, nodes, nodesExcluded, requested)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nodemanager.Node), args.Error(1)
}

func TestPlaceSandbox_SuccessfulPlacement(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("")

	// Create test nodes
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 5, 4)
	node3 := nodemanager.NewTestNode("node3", api.NodeStatusReady, 7, 4)
	nodes := []*nodemanager.Node{node1, node2, node3}

	// Create a mock algorithm that returns node2
	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything).
		Return(node2, nil)

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, tracer, algorithm, nodes, nil, sbxRequest)

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode)
	algorithm.AssertExpectations(t)
}

func TestPlaceSandbox_WithPreferredNode(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("")

	// Create test nodes
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 5, 4)
	node3 := nodemanager.NewTestNode("node3", api.NodeStatusReady, 7, 4)
	nodes := []*nodemanager.Node{node1, node2, node3}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	// Test without preferred node - algorithm should be called
	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything).
		Return(node1, nil).Once()

	resultNode, err := PlaceSandbox(ctx, tracer, algorithm, nodes, nil, sbxRequest)
	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node1, resultNode)
	algorithm.AssertExpectations(t)

	// Test with preferred node - should use the preferred node directly without calling algorithm
	resultNode, err = PlaceSandbox(ctx, tracer, algorithm, nodes, node2, sbxRequest)
	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode)
	// Algorithm should not be called when preferred node is provided
	algorithm.AssertNotCalled(t, "chooseNode")
}

func TestPlaceSandbox_ContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	tracer := noop.NewTracerProvider().Tracer("")

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			// Simulate slow node selection
			time.Sleep(10 * time.Millisecond)
		}).
		Return(nil, errors.New("timeout"))

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, tracer, algorithm, []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4),
	}, nil, sbxRequest)

	require.Error(t, err)
	assert.Nil(t, resultNode)
	// The error could be either "timeout" from the algorithm or "request timed out" from ctx.Done()
	assert.True(t, err.Error() == "timeout" || errors.Is(err, context.DeadlineExceeded))
}

func TestPlaceSandbox_NoNodes(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("")

	algorithm := &mockAlgorithm{}
	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, tracer, algorithm, []*nodemanager.Node{}, nil, sbxRequest)

	require.Error(t, err)
	assert.Nil(t, resultNode)
	assert.Contains(t, err.Error(), "no nodes available")
}

func TestPlaceSandbox_AllNodesExcluded(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("")

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("no nodes available"))

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, tracer, algorithm, []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4),
	}, nil, sbxRequest)

	require.Error(t, err)
	assert.Nil(t, resultNode)
	assert.Contains(t, err.Error(), "no nodes available")
	algorithm.AssertExpectations(t)
}
