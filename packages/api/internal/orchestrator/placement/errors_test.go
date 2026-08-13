package placement

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

func TestPlaceSandbox_TimeoutReturnsTypedError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	nodes := []*nodemanager.Node{nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4)}

	_, err := PlaceSandbox(ctx, failIfCalled(t), nodes, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var timeoutErr PlacementTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, 0, timeoutErr.Attempts)
}

func TestPlaceSandbox_DeadlineDuringFinalAttemptReturnsTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	nodes := []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "create failed on first node"))),
		nodemanager.NewTestNode("node2", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "create failed on second node"))),
		nodemanager.NewTestNode("node3", api.NodeStatusReady, 0, 4),
	}
	nodes[2].SetSandboxClient(erroringClient(cancel, status.Error(codes.DeadlineExceeded, "context deadline exceeded")))

	algorithm := stubAlgorithm{choose: func(excluded map[string]struct{}) (*nodemanager.Node, error) {
		for _, n := range nodes {
			if _, ok := excluded[n.ID]; !ok {
				return n, nil
			}
		}

		return nil, errors.New("all nodes excluded")
	}}

	result, err := PlaceSandbox(ctx, algorithm, nodes, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var timeoutErr PlacementTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, 3, timeoutErr.Attempts)
	assert.True(t, result.TimedOut)
}

func TestPlaceSandbox_NoNodesReturnsTypedError(t *testing.T) {
	t.Parallel()

	_, err := PlaceSandbox(t.Context(), failIfCalled(t), nil, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var noNodesErr NoNodesAvailableError
	require.ErrorAs(t, err, &noNodesErr)
}

func TestPlaceSandbox_CapacitySpikeToDeadlineClassifiedAsCapacity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	node := nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4)
	node.SetSandboxClient(erroringClient(cancel, status.Error(codes.ResourceExhausted, "no capacity")))

	result, err := PlaceSandbox(ctx, failIfCalled(t), []*nodemanager.Node{node}, node, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var noNodesErr NoNodesAvailableError
	require.ErrorAs(t, err, &noNodesErr)
	assert.True(t, result.TimedOut)
	assert.Equal(t, "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly", ClientMessage(err))
}

func TestPlaceSandbox_HardFailureThenRefusalsToDeadlineStaysTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	hard := nodemanager.NewTestNode("node-hard", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "create failed")))
	exhausted := nodemanager.NewTestNode("node-exhausted", api.NodeStatusReady, 0, 4)
	exhausted.SetSandboxClient(erroringClient(cancel, status.Error(codes.ResourceExhausted, "no capacity")))

	algorithm := stubAlgorithm{choose: func(map[string]struct{}) (*nodemanager.Node, error) {
		return exhausted, nil
	}}

	_, err := PlaceSandbox(ctx, algorithm, []*nodemanager.Node{hard, exhausted}, hard, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var timeoutErr PlacementTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, 1, timeoutErr.Attempts)
}

func TestPlaceSandbox_AllExcludedForwardsLastCreateError(t *testing.T) {
	t.Parallel()

	nodes := []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.FailedPrecondition, "sandbox files for 'abc' not found"))),
	}

	algorithm := stubAlgorithm{choose: func(excluded map[string]struct{}) (*nodemanager.Node, error) {
		for _, n := range nodes {
			if _, ok := excluded[n.ID]; !ok {
				return n, nil
			}
		}

		return nil, errors.New("all nodes excluded")
	}}

	_, err := PlaceSandbox(t.Context(), algorithm, nodes, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var createErr SandboxCreateError
	require.ErrorAs(t, err, &createErr)
	assert.Equal(t, 1, createErr.Attempts)
	assert.Equal(t, "Failed to place sandbox: sandbox files for 'abc' not found", ClientMessage(err))
}

func TestPlaceSandbox_NoEligibleNodeReturnsTypedError(t *testing.T) {
	t.Parallel()

	nodes := []*nodemanager.Node{nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4)}
	algorithm := stubAlgorithm{choose: func(map[string]struct{}) (*nodemanager.Node, error) {
		return nil, FailedToPlaceSandboxError{}
	}}

	_, err := PlaceSandbox(t.Context(), algorithm, nodes, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var noEligibleErr FailedToPlaceSandboxError
	require.ErrorAs(t, err, &noEligibleErr)
}

func TestPlaceSandbox_RetriesExhaustedKeepsLastError(t *testing.T) {
	t.Parallel()

	nodes := []*nodemanager.Node{
		nodemanager.NewTestNode("node1", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "create failed on first node"))),
		nodemanager.NewTestNode("node2", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.Internal, "create failed on second node"))),
		nodemanager.NewTestNode("node3", api.NodeStatusReady, 0, 4, nodemanager.WithSandboxCreateError(status.Error(codes.FailedPrecondition, "sandbox files for 'abc' not found"))),
	}

	algorithm := stubAlgorithm{choose: func(excluded map[string]struct{}) (*nodemanager.Node, error) {
		for _, n := range nodes {
			if _, ok := excluded[n.ID]; !ok {
				return n, nil
			}
		}

		return nil, errors.New("all nodes excluded")
	}}

	_, err := PlaceSandbox(t.Context(), algorithm, nodes, nil, testSbxRequest("test-sandbox"), machineinfo.MachineInfo{}, false, nil)

	var createErr SandboxCreateError
	require.ErrorAs(t, err, &createErr)
	assert.Equal(t, maxRetries, createErr.Attempts)
	assert.Equal(t, codes.FailedPrecondition, status.Code(createErr.LastErr))
	assert.Contains(t, createErr.LastErr.Error(), "sandbox files for 'abc' not found")
}

func TestClientMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "timeout",
			err:  PlacementTimeoutError{Attempts: 2},
			want: "Failed to place sandbox: placement timed out after 2 attempt(s), please retry",
		},
		{
			name: "zero-attempt timeout has no counter",
			err:  PlacementTimeoutError{Attempts: 0},
			want: "Failed to place sandbox: placement timed out, please retry",
		},
		{
			name: "no nodes available",
			err:  NoNodesAvailableError{},
			want: "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly",
		},
		{
			name: "no eligible node hides machine info and labels",
			err: FailedToPlaceSandboxError{
				filterByLabels:   true,
				requiredLabels:   []string{"gpu"},
				buildMachineInfo: machineinfo.MachineInfo{CPUArchitecture: "x86_64", CPUFlags: []string{"avx512f"}},
			},
			want: "Failed to place sandbox: no compatible node for this template's requirements",
		},
		{
			name: "create failed forwards safe status message",
			err:  SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.FailedPrecondition, "sandbox files for 'abc' not found")},
			want: "Failed to place sandbox: sandbox files for 'abc' not found",
		},
		{
			name: "create failed forwards invalid-argument message",
			err:  SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.InvalidArgument, "invalid volume mount path")},
			want: "Failed to place sandbox: invalid volume mount path",
		},
		{
			name: "create failed hides unsafe status message",
			err:  SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.Internal, "node ip-10-0-0-1: disk full")},
			want: "Failed to place sandbox: sandbox creation failed on 3 node(s), please retry; if the problem persists, contact us",
		},
		{
			name: "create failed hides non-grpc error",
			err:  SandboxCreateError{Attempts: 2, LastErr: errors.New("dial tcp 10.0.0.1: connection refused")},
			want: "Failed to place sandbox: sandbox creation failed on 2 node(s), please retry; if the problem persists, contact us",
		},
		{
			name: "wrapped typed error still classified",
			err:  fmt.Errorf("failed to place sandbox: %w", NoNodesAvailableError{}),
			want: "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly",
		},
		{
			name: "unknown error keeps generic message",
			err:  errors.New("boom"),
			want: "Failed to place sandbox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, ClientMessage(tt.err))
		})
	}
}

func TestPlaceSandbox_ChooseFailureAfterCreateAttemptForwardsCreateError(t *testing.T) {
	t.Parallel()

	failing := nodemanager.NewTestNode("node-failing", api.NodeStatusReady, 0, 4,
		nodemanager.WithSandboxCreateError(status.Error(codes.FailedPrecondition, "sandbox files for 'sbx-1' not found")))
	notReady := nodemanager.NewTestNode("node-draining", api.NodeStatusDraining, 0, 4)

	algorithm := stubAlgorithm{choose: func(excluded map[string]struct{}) (*nodemanager.Node, error) {
		if _, ok := excluded[failing.ID]; !ok {
			return failing, nil
		}

		return nil, FailedToPlaceSandboxError{}
	}}

	_, err := PlaceSandbox(t.Context(), algorithm, []*nodemanager.Node{failing, notReady}, nil, testSbxRequest("sbx-1"), machineinfo.MachineInfo{}, false, nil)

	var createErr SandboxCreateError
	require.ErrorAs(t, err, &createErr)
	assert.Equal(t, 1, createErr.Attempts)
	assert.Equal(t, "Failed to place sandbox: sandbox files for 'sbx-1' not found", ClientMessage(err))
}
