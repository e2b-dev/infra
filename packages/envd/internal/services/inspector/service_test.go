package inspector

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/inspector"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	logger := zerolog.Nop()
	return newService(&logger, nil, Config{})
}

// On non-linux builds (or linux without -tags inspector_bpf) the stub
// fsTracker is in use, so QueryChanges always reports degraded=true.
// On linux+inspector_bpf builds the BPF tracker may be active; this
// test is permissive on degraded and only asserts the contract that
// degraded ⇒ filesystem_changed=false.
func TestQueryChangesDegradedContract(t *testing.T) {
	s := newTestService(t)

	resp, err := s.QueryChanges(context.Background(), connect.NewRequest(&rpc.QueryChangesRequest{}))
	require.NoError(t, err)

	if resp.Msg.GetDegraded() {
		// Degraded responses MUST report "no changes" alongside the
		// degraded flag — the orchestrator falls through to a full
		// checkpoint based on the degraded bit alone.
		assert.False(t, resp.Msg.GetFilesystemChanged(),
			"degraded response must not also assert filesystem_changed")
	}
	assert.False(t, resp.Msg.GetProcessesChanged(), "PR 3 has not landed yet")
}

func TestResetEpochIncrements(t *testing.T) {
	s := newTestService(t)

	first, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
	require.NoError(t, err)
	assert.Equal(t, uint32(1), first.Msg.GetNewEpochId())

	second, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
	require.NoError(t, err)
	assert.Equal(t, uint32(2), second.Msg.GetNewEpochId())
}

func TestResetEpochRejectsStaleExpectedID(t *testing.T) {
	s := newTestService(t)

	_, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
	require.NoError(t, err)

	_, err = s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{ExpectedEpochId: 0xdeadbeef}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
}

func TestResetEpochAcceptsZeroExpectedAsForce(t *testing.T) {
	s := newTestService(t)

	for range 3 {
		_, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
		require.NoError(t, err)
	}

	resp, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{ExpectedEpochId: 0}))
	require.NoError(t, err)
	assert.Equal(t, uint32(4), resp.Msg.GetNewEpochId())
}

func TestStatusReportsDegradedReasonWhenNotLoaded(t *testing.T) {
	s := newTestService(t)

	resp, err := s.Status(context.Background(), connect.NewRequest(&rpc.StatusRequest{}))
	require.NoError(t, err)

	// Non-loaded tracker (stub or kernel-load failure) must surface a
	// non-empty degraded_reason.
	if !resp.Msg.GetBpfLoaded() {
		assert.NotEmpty(t, resp.Msg.GetDegradedReason())
	}
}
