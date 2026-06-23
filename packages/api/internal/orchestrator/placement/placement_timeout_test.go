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
			t.Fatal("chooseNode should not be called")

			return nil, nil
		},
	}
}

// erroringClient returns err from SandboxCreate, optionally cancelling the
// request context first to simulate the deadline being hit mid-create.
func erroringClient(cancel context.CancelFunc, err error) *nodemanager.MockSandboxClientCustom {
	return &nodemanager.MockSandboxClientCustom{
		CreateFunc: func() error {
			if cancel != nil {
				cancel()
			}

			return err
		},
	}
}

// TestPlaceSandbox_TimeoutPinsFirstTriedNode is the core prod scenario: a node
// fails (here with codes.Internal, the code the orchestrator returns for a
// timed-out resume) and the request context is cancelled. PlaceSandbox must
// surface that node so a retry can be pinned to it - detection must NOT depend
// on the gRPC code being DeadlineExceeded/Canceled.
func TestPlaceSandbox_TimeoutPinsFirstTriedNode(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	node := nodemanager.NewTestNode("node-test", api.NodeStatusReady, 0, 8)
	node.SetSandboxClient(erroringClient(cancel, status.Error(codes.Internal, "failed to create sandbox: request timed out")))

	_, err := PlaceSandbox(
		ctx,
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-1"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	require.True(t, errors.As(err, &timeoutErr), "expected a *PlacementTimeoutError, got %v", err)
	require.NotNil(t, timeoutErr.Node)
	assert.Equal(t, node.ID, timeoutErr.Node.ID)
}

// TestPlaceSandbox_PinsFirstTriedNodeNotLater verifies that across multiple
// attempts the FIRST node tried is the one surfaced, not a later one.
func TestPlaceSandbox_PinsFirstTriedNodeNotLater(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	first := nodemanager.NewTestNode("node-first", api.NodeStatusReady, 0, 8)
	// First node fails without cancelling - a long pull that the orchestrator
	// gave up on while the request budget was still alive.
	first.SetSandboxClient(erroringClient(nil, status.Error(codes.Internal, "boom")))

	second := nodemanager.NewTestNode("node-second", api.NodeStatusReady, 0, 8)
	// Second node fails and the request budget runs out here.
	second.SetSandboxClient(erroringClient(cancel, status.Error(codes.Internal, "boom")))

	algo := stubAlgorithm{choose: func(map[string]struct{}) (*nodemanager.Node, error) {
		return second, nil
	}}

	_, err := PlaceSandbox(
		ctx,
		algo,
		[]*nodemanager.Node{first, second},
		first,
		testSbxRequest("sbx-2"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	var timeoutErr *PlacementTimeoutError
	require.True(t, errors.As(err, &timeoutErr))
	require.NotNil(t, timeoutErr.Node)
	assert.Equal(t, first.ID, timeoutErr.Node.ID, "must pin the first node tried, not a later one")
}

// TestPlaceSandbox_HardFailureNotWrapped verifies a failure where the context is
// still live (a genuine error, not a timeout) is returned unchanged, so a retry
// is never pinned to a node that actually refused the sandbox.
func TestPlaceSandbox_HardFailureNotWrapped(t *testing.T) {
	t.Parallel()

	node := nodemanager.NewTestNode("node-internal", api.NodeStatusReady, 0, 8)
	node.SetSandboxClient(erroringClient(nil, status.Error(codes.Internal, "boom")))

	_, err := PlaceSandbox(
		t.Context(),
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-3"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	require.Error(t, err)

	var timeoutErr *PlacementTimeoutError
	assert.False(t, errors.As(err, &timeoutErr), "a live-context failure must not be wrapped as a timeout")
}

// TestPlaceSandbox_ResourceExhaustedNotPinned verifies that a node which refused
// fast with ResourceExhausted is not pinned even if the request then times out.
func TestPlaceSandbox_ResourceExhaustedNotPinned(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	node := nodemanager.NewTestNode("node-exhausted", api.NodeStatusReady, 0, 8)
	node.SetSandboxClient(erroringClient(cancel, status.Error(codes.ResourceExhausted, "no capacity")))

	_, err := PlaceSandbox(
		ctx,
		failIfCalled(t),
		[]*nodemanager.Node{node},
		node,
		testSbxRequest("sbx-4"),
		machineinfo.MachineInfo{},
		false,
		nil,
	)

	require.Error(t, err)

	var timeoutErr *PlacementTimeoutError
	assert.False(t, errors.As(err, &timeoutErr), "a node that refused fast must not be pinned")
}

// TestPlaceSandbox_TimeoutBeforeAnyAttemptNotWrapped verifies that when the
// deadline fires before any node was tried there is nothing to pin to.
func TestPlaceSandbox_TimeoutBeforeAnyAttemptNotWrapped(t *testing.T) {
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
	assert.False(t, errors.As(err, &timeoutErr), "no node was tried, so there is nothing to pin")
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
