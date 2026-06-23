package placement

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

// stubAlgorithm is a placement Algorithm whose chooseNode behavior is injected.
type stubAlgorithm struct {
	choose func(nodesExcluded map[string]struct{}) (*nodemanager.Node, error)
}

func (s stubAlgorithm) chooseNode(
	_ context.Context,
	_ []*nodemanager.Node,
	nodesExcluded map[string]struct{},
	_ nodemanager.SandboxResources,
	_ machineinfo.MachineInfo,
	_ bool,
	_ []string,
) (*nodemanager.Node, error) {
	return s.choose(nodesExcluded)
}

func testSbxRequest(id string) *orchestrator.SandboxCreateRequest {
	return &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId: id,
			Vcpu:      2,
			RamMb:     512,
		},
	}
}

// failIfCalled is an algorithm that fails the test if chooseNode is invoked.
func failIfCalled(t *testing.T) stubAlgorithm {
	t.Helper()

	return stubAlgorithm{
		choose: func(map[string]struct{}) (*nodemanager.Node, error) {
			t.Fatal("chooseNode should not be called when a preferred node is supplied")

			return nil, nil
		},
	}
}

func TestPlaceSandbox_TimeoutSurfacesInFlightNode(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-timeout", api.NodeStatusReady, 0, 8,
		nodemanager.WithSandboxCreateError(status.Error(codes.DeadlineExceeded, "deadline exceeded")))

	placed, err := PlaceSandbox(
		t.Context(),
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-1"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	assert.Nil(t, placed)

	var timeoutErr *PlacementTimeoutError
	require.True(t, errors.As(err, &timeoutErr), "expected a *PlacementTimeoutError, got %v", err)
	require.NotNil(t, timeoutErr.Node)
	assert.Equal(t, node.ID, timeoutErr.Node.ID, "timeout error must carry the node that was mid-creation")
}

func TestPlaceSandbox_CanceledSurfacesInFlightNode(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-canceled", api.NodeStatusReady, 0, 8,
		nodemanager.WithSandboxCreateError(status.Error(codes.Canceled, "canceled")))

	_, err := PlaceSandbox(
		t.Context(),
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-2"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	require.True(t, errors.As(err, &timeoutErr))
	require.NotNil(t, timeoutErr.Node)
	assert.Equal(t, node.ID, timeoutErr.Node.ID)
}

func TestPlaceSandbox_ResourceExhaustedIsNotTimeout(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-exhausted", api.NodeStatusReady, 0, 8,
		nodemanager.WithSandboxCreateError(status.Error(codes.ResourceExhausted, "no capacity")))

	// ResourceExhausted does not exclude the node and does not increment the
	// attempt, so PlaceSandbox loops back into the algorithm. Terminate there.
	errNoNode := errors.New("no node available")
	algo := stubAlgorithm{choose: func(map[string]struct{}) (*nodemanager.Node, error) {
		return nil, errNoNode
	}}

	_, err := PlaceSandbox(
		t.Context(),
		algo,
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-3"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	assert.False(t, errors.As(err, &timeoutErr), "resource exhaustion must not produce a timeout error")
	assert.ErrorIs(t, err, errNoNode)
}

func TestPlaceSandbox_OtherErrorIsNotTimeout(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-internal", api.NodeStatusReady, 0, 8,
		nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "boom")))

	// An internal error excludes the node; with a single cluster node the loop
	// then runs out of nodes.
	_, err := PlaceSandbox(
		t.Context(),
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-4"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	assert.False(t, errors.As(err, &timeoutErr), "hard errors must not produce a timeout error")
	require.Error(t, err)
}

func TestPlaceSandbox_DeadlineBeforeAnyAttemptIsNotWrapped(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-ctx", api.NodeStatusReady, 0, 8)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already past deadline before the first attempt

	_, err := PlaceSandbox(
		ctx,
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-5"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	require.Error(t, err)

	var timeoutErr *PlacementTimeoutError
	assert.False(t, errors.As(err, &timeoutErr),
		"no node was tried, so there is nothing to pin a retry to")
}

// TestPlaceSandbox_CtxDoneSurfacesFirstTriedNode exercises the ctx.Done() return
// path: a node times out during creation (setting the first tried node) and
// cancels the request context, so the next loop iteration bails out at the top
// and must still surface the warming node.
func TestPlaceSandbox_CtxDoneSurfacesFirstTriedNode(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	node := nodemanager.NewTestNode("node-inflight", api.NodeStatusReady, 0, 8)
	// A second ready node keeps "no nodes available" from short-circuiting, so
	// the loop reaches the top-of-loop ctx.Done() check on the next iteration.
	other := nodemanager.NewTestNode("node-other", api.NodeStatusReady, 0, 8)

	node.SetSandboxClient(&nodemanager.MockSandboxClientCustom{
		CreateFunc: func() error {
			cancel() // request deadline fires while this node is mid-creation

			return status.Error(codes.DeadlineExceeded, "deadline exceeded")
		},
	})

	_, err := PlaceSandbox(
		ctx,
		failIfCalled(t),
		[]*nodemanager.Node{node, other},
		node,
		testSbxRequest("sbx-7"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	require.True(t, errors.As(err, &timeoutErr), "expected a *PlacementTimeoutError, got %v", err)
	require.NotNil(t, timeoutErr.Node)
	assert.Equal(t, node.ID, timeoutErr.Node.ID)
}

func TestPlaceSandbox_SuccessReturnsNode(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-ok", api.NodeStatusReady, 0, 8)

	placed, err := PlaceSandbox(
		t.Context(),
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-6"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, placed)
	assert.Equal(t, node.ID, placed.ID)
}
