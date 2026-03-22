package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

func boolPtr(b bool) *bool { return &b }

func TestIsNonEnvdTrafficRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		md       metadata.MD
		expected bool
	}{
		{
			name:     "no port metadata returns true",
			md:       metadata.MD{},
			expected: true,
		},
		{
			name:     "envd port returns false",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "49983"),
			expected: false,
		},
		{
			name:     "non-envd port returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "8080"),
			expected: true,
		},
		{
			name:     "invalid port string returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "not-a-number"),
			expected: true,
		},
		{
			name:     "empty port string returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, ""),
			expected: true,
		},
		{
			name:     "port 0 returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "0"),
			expected: true,
		},
		{
			name:     "port 443 returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "443"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isNonEnvdTrafficRequest(context.Background(), tt.md, "test-sandbox")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsPrivateIngressTraffic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		network  *dbtypes.SandboxNetworkConfig
		expected bool
	}{
		{
			name:     "nil network",
			network:  nil,
			expected: false,
		},
		{
			name:     "nil ingress",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: nil},
			expected: false,
		},
		{
			name:     "nil AllowPublicAccess",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: nil}},
			expected: false,
		},
		{
			name:     "AllowPublicAccess true",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: boolPtr(true)}},
			expected: false,
		},
		{
			name:     "AllowPublicAccess false",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: boolPtr(false)}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isPrivateIngressTraffic(tt.network)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokensMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provided string
		expected string
		match    bool
	}{
		{
			name:     "identical tokens match",
			provided: "secret-token-123",
			expected: "secret-token-123",
			match:    true,
		},
		{
			name:     "different tokens do not match",
			provided: "wrong-token",
			expected: "secret-token-123",
			match:    false,
		},
		{
			name:     "empty provided does not match",
			provided: "",
			expected: "secret-token-123",
			match:    false,
		},
		{
			name:     "empty expected does not match",
			provided: "secret-token-123",
			expected: "",
			match:    false,
		},
		{
			name:     "both empty match",
			provided: "",
			expected: "",
			match:    true,
		},
		{
			name:     "different length tokens do not match",
			provided: "short",
			expected: "much-longer-token-value",
			match:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tokensMatch(tt.provided, tt.expected)
			assert.Equal(t, tt.match, result)
		})
	}
}

func testSandboxForAutoResume(state sandbox.State) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID: "test-sandbox",
		State:     state,
		NodeID:    "node-1",
		ClusterID: uuid.New(),
	}
}

func TestHandleExistingSandboxAutoResume(t *testing.T) {
	t.Parallel()

	t.Run("running sandbox returns node ip immediately", func(t *testing.T) {
		t.Parallel()

		waitCalled := false
		nodeCalls := 0
		nodeIP, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StateRunning),
			func(context.Context) error {
				waitCalled = true

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called for running sandbox")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				nodeCalls++

				return "10.0.0.1", nil
			},
		)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Equal(t, "10.0.0.1", nodeIP)
		assert.False(t, waitCalled)
		assert.Equal(t, 1, nodeCalls)
	})

	t.Run("pausing sandbox waits and routes when refreshed sandbox is running", func(t *testing.T) {
		t.Parallel()

		waitCalls := 0
		refreshedSandbox := testSandboxForAutoResume(sandbox.StateRunning)
		refreshedSandbox.NodeID = "node-2"
		nodeCalls := 0
		nodeIP, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StatePausing),
			func(context.Context) error {
				waitCalls++

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				return refreshedSandbox, nil
			},
			func(sbx sandbox.Sandbox) (string, error) {
				nodeCalls++
				assert.Equal(t, refreshedSandbox.ClusterID, sbx.ClusterID)
				assert.Equal(t, refreshedSandbox.NodeID, sbx.NodeID)

				return "10.0.0.1", nil
			},
		)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Equal(t, "10.0.0.1", nodeIP)
		assert.Equal(t, 1, waitCalls)
		assert.Equal(t, 1, nodeCalls)
	})

	t.Run("pausing sandbox falls back to resume flow when refreshed sandbox lookup fails", func(t *testing.T) {
		t.Parallel()

		waitCalls := 0
		nodeCalled := false
		nodeIP, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StatePausing),
			func(context.Context) error {
				waitCalls++

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", "test-sandbox", sandbox.ErrNotFound)
			},
			func(sandbox.Sandbox) (string, error) {
				nodeCalled = true

				return "10.0.0.1", nil
			},
		)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.Empty(t, nodeIP)
		assert.Equal(t, 1, waitCalls)
		assert.False(t, nodeCalled)
	})

	t.Run("pausing sandbox wait failure returns internal error", func(t *testing.T) {
		t.Parallel()

		waitErr := errors.New("boom")
		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StatePausing),
			func(context.Context) error {
				return waitErr
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called when wait fails")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called when wait fails")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, "error waiting for sandbox to pause", st.Message())
	})

	t.Run("killing sandbox returns not found", func(t *testing.T) {
		t.Parallel()

		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StateKilling),
			func(context.Context) error {
				t.Fatal("waitForStateChange should not be called for killing sandbox")

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called for killing sandbox")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called for killing sandbox")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, "sandbox not found", st.Message())
	})

	t.Run("snapshotting sandbox waits and routes when refreshed sandbox is running", func(t *testing.T) {
		t.Parallel()

		waitCalls := 0
		refreshedSandbox := testSandboxForAutoResume(sandbox.StateRunning)
		refreshedSandbox.NodeID = "node-3"
		nodeCalls := 0
		nodeIP, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StateSnapshotting),
			func(context.Context) error {
				waitCalls++

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				return refreshedSandbox, nil
			},
			func(sbx sandbox.Sandbox) (string, error) {
				nodeCalls++
				assert.Equal(t, refreshedSandbox.ClusterID, sbx.ClusterID)
				assert.Equal(t, refreshedSandbox.NodeID, sbx.NodeID)

				return "10.0.0.2", nil
			},
		)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Equal(t, "10.0.0.2", nodeIP)
		assert.Equal(t, 1, waitCalls)
		assert.Equal(t, 1, nodeCalls)
	})

	t.Run("snapshotting sandbox wait failure returns internal error", func(t *testing.T) {
		t.Parallel()

		waitErr := errors.New("boom")
		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StateSnapshotting),
			func(context.Context) error {
				return waitErr
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called when snapshot wait fails")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called when snapshot wait fails")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, "error waiting for sandbox snapshot to finish", st.Message())
	})

	t.Run("pausing sandbox returns internal error when refreshed sandbox lookup fails unexpectedly", func(t *testing.T) {
		t.Parallel()

		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StatePausing),
			func(context.Context) error {
				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				return sandbox.Sandbox{}, errors.New("redis unavailable")
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called when refresh fails")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Equal(t, "failed to refresh sandbox state: redis unavailable", st.Message())
	})

	t.Run("running sandbox returns routing error when node ip lookup fails", func(t *testing.T) {
		t.Parallel()

		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StateRunning),
			func(context.Context) error {
				t.Fatal("waitForStateChange should not be called for running sandbox")

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called for running sandbox")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				return "", status.Error(codes.Internal, "sandbox is running but routing info is not available yet")
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Equal(t, "sandbox is running but routing info is not available yet", st.Message())
	})

	t.Run("unknown sandbox state returns internal error", func(t *testing.T) {
		t.Parallel()

		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.State("mystery")),
			func(context.Context) error {
				t.Fatal("waitForStateChange should not be called for unknown sandbox state")

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				t.Fatal("getSandbox should not be called for unknown sandbox state")

				return sandbox.Sandbox{}, nil
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called for unknown sandbox state")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Equal(t, "sandbox is in an unknown state", st.Message())
	})

	t.Run("sandbox still transitioning after max retries returns failed precondition", func(t *testing.T) {
		t.Parallel()

		waitCalls := 0
		getSandboxCalls := 0
		_, handled, err := handleExistingSandboxAutoResume(
			t.Context(),
			"test-sandbox",
			testSandboxForAutoResume(sandbox.StatePausing),
			func(context.Context) error {
				waitCalls++

				return nil
			},
			func(context.Context) (sandbox.Sandbox, error) {
				getSandboxCalls++

				return testSandboxForAutoResume(sandbox.StatePausing), nil
			},
			func(sandbox.Sandbox) (string, error) {
				t.Fatal("getNodeIP should not be called while sandbox is still transitioning")

				return "", nil
			},
		)
		require.Error(t, err)
		assert.False(t, handled)
		assert.Equal(t, maxAutoResumeTransitionRetries, waitCalls)
		assert.Equal(t, maxAutoResumeTransitionRetries, getSandboxCalls)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Equal(t, "sandbox is still transitioning", st.Message())
	})
}
