package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

type mockAlgorithm struct {
	mock.Mock
}

func (m *mockAlgorithm) chooseNode(ctx context.Context, nodes []*nodemanager.Node, nodesExcluded map[string]struct{}, requested nodemanager.SandboxResources, buildCPUInfo machineinfo.MachineInfo) (*nodemanager.Node, error) {
	args := m.Called(ctx, nodes, nodesExcluded, requested, buildCPUInfo)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*nodemanager.Node), args.Error(1)
}

func TestPlaceSandbox_SuccessfulPlacement(t *testing.T) {
	ctx := t.Context()

	// Create test nodes
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4)
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 5, 4)
	node3 := nodemanager.NewTestNode("node3", api.NodeStatusReady, 7, 4)
	nodes := []*nodemanager.Node{node1, node2, node3}

	// Create a mock algorithm that returns node2
	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node2, nil)

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, nil, sbxRequest, machineinfo.MachineInfo{})

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode)
	algorithm.AssertExpectations(t)
}

func TestPlaceSandbox_WithPreferredNode(t *testing.T) {
	ctx := t.Context()

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
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node1, nil).Once()

	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, nil, sbxRequest, machineinfo.MachineInfo{})
	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node1, resultNode)
	algorithm.AssertExpectations(t)

	// Test with preferred node - should use the preferred node directly without calling algorithm
	resultNode, err = PlaceSandbox(ctx, algorithm, nodes, node2, sbxRequest, machineinfo.MachineInfo{})
	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode)
	// Algorithm should not be called when preferred node is provided
	algorithm.AssertNotCalled(t, "chooseNode")
}

func TestPlaceSandbox_ContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
	defer cancel()

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
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

	resultNode, err := PlaceSandbox(ctx, algorithm, []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4),
	}, nil, sbxRequest, machineinfo.MachineInfo{})

	require.Error(t, err)
	assert.Nil(t, resultNode)
	// The error could be either "timeout" from the algorithm or "request timed out" from ctx.Done()
	assert.True(t, err.Error() == "timeout" || strings.Contains(err.Error(), "request timed out"))
}

func TestPlaceSandbox_NoNodes(t *testing.T) {
	ctx := t.Context()

	algorithm := &mockAlgorithm{}
	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, algorithm, []*nodemanager.Node{}, nil, sbxRequest, machineinfo.MachineInfo{})

	require.Error(t, err)
	assert.Nil(t, resultNode)
	assert.Contains(t, err.Error(), "no nodes available")
}

func TestPlaceSandbox_AllNodesExcluded(t *testing.T) {
	ctx := t.Context()

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("no nodes available"))

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, algorithm, []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4),
	}, nil, sbxRequest, machineinfo.MachineInfo{})

	require.Error(t, err)
	assert.Nil(t, resultNode)
	assert.Contains(t, err.Error(), "no nodes available")
	algorithm.AssertExpectations(t)
}

func TestPlaceSandbox_ResourceExhausted(t *testing.T) {
	ctx := t.Context()

	// Create test nodes - node1 will return ResourceExhausted, node2 will succeed
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4,
		nodemanager.WithSandboxCreateError(status.Error(codes.ResourceExhausted, "node exhausted")))
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 5, 4)
	nodes := []*nodemanager.Node{node1, node2}

	// Algorithm should be called twice - first returns node1 (exhausted), then node2 (succeeds)
	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node1, nil).Once()
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node2, nil).Once()

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, nil, sbxRequest, machineinfo.MachineInfo{})

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode, "should succeed on node2 after node1 was exhausted")
	algorithm.AssertExpectations(t)

	// Verify node1 was NOT excluded (ResourceExhausted nodes should be retried)
	algorithm.AssertNumberOfCalls(t, "chooseNode", 2)
}

func TestPlaceSandbox_NotFoundWithPreferredNode(t *testing.T) {
	ctx := t.Context()

	// Scenario: Preferred node is exhausted, we try another node which returns NotFound,
	// then we should retry on the preferred node

	// Create a mock client that returns ResourceExhausted first, then succeeds on retry
	callCount := 0

	preferredNode := nodemanager.NewTestNode("preferred-node", api.NodeStatusReady, 5, 4)
	preferredNode.SetSandboxClient(&nodemanager.MockSandboxClientCustom{
		CreateFunc: func() error {
			callCount++
			if callCount == 1 {
				return status.Error(codes.ResourceExhausted, "node temporarily exhausted")
			}

			return nil
		},
	})

	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4,
		nodemanager.WithSandboxCreateError(status.Error(codes.NotFound, "sandbox files not found")))
	nodes := []*nodemanager.Node{preferredNode, node1}

	// Algorithm should be called once to select node1 after preferred node is exhausted
	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node1, nil).Once()

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	// Start with preferred node (exhausted) -> try node1 (NotFound) -> retry preferred node (succeeds)
	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, preferredNode, sbxRequest, machineinfo.MachineInfo{})

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, preferredNode, resultNode, "should retry on preferred node after NotFound on different node")

	// Algorithm should be called once (for node1), then preferred node is retried
	algorithm.AssertNumberOfCalls(t, "chooseNode", 1)
	assert.Equal(t, 2, callCount, "preferred node should be tried twice")
}

func TestPlaceSandbox_NotFoundWithoutPreferredNode(t *testing.T) {
	ctx := t.Context()

	// Create test nodes - both return NotFound initially, node2 succeeds on retry
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4,
		nodemanager.WithSandboxCreateError(status.Error(codes.NotFound, "sandbox files not found")))
	node2 := nodemanager.NewTestNode("node2", api.NodeStatusReady, 5, 4)
	nodes := []*nodemanager.Node{node1, node2}

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node1, nil).Once()
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node2, nil).Once()

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	// No preferred node - should retry with algorithm
	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, nil, sbxRequest, machineinfo.MachineInfo{})

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node2, resultNode, "should succeed on node2 after NotFound on node1")
	algorithm.AssertNumberOfCalls(t, "chooseNode", 2)
}

func TestPlaceSandbox_NotFoundOnPreferredNode(t *testing.T) {
	ctx := t.Context()

	// Create test nodes - preferred node returns NotFound, node1 succeeds
	preferredNode := nodemanager.NewTestNode("preferred-node", api.NodeStatusReady, 5, 4,
		nodemanager.WithSandboxCreateError(status.Error(codes.NotFound, "sandbox files not found")))
	node1 := nodemanager.NewTestNode("node1", api.NodeStatusReady, 3, 4)
	nodes := []*nodemanager.Node{preferredNode, node1}

	algorithm := &mockAlgorithm{}
	algorithm.On("chooseNode", mock.Anything, nodes, mock.Anything, mock.Anything, mock.Anything).
		Return(node1, nil).Once()

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: "test-sandbox",
			Vcpu:      2,
			RamMb:     1024,
		},
	}

	// Start with preferred node that returns NotFound
	resultNode, err := PlaceSandbox(ctx, algorithm, nodes, preferredNode, sbxRequest, machineinfo.MachineInfo{})

	require.NoError(t, err)
	assert.NotNil(t, resultNode)
	assert.Equal(t, node1, resultNode, "should succeed on node1 after NotFound on preferred node")
	algorithm.AssertNumberOfCalls(t, "chooseNode", 1)
}
