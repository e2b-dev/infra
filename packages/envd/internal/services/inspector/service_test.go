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
	return newService(&logger, nil)
}

func TestQueryChangesScaffoldIsDegraded(t *testing.T) {
	s := newTestService(t)

	resp, err := s.QueryChanges(context.Background(), connect.NewRequest(&rpc.QueryChangesRequest{}))
	require.NoError(t, err)

	// The scaffold MUST report degraded so callers fall through to a full
	// checkpoint. PR 2 / PR 3 will flip this once the trackers ship.
	assert.True(t, resp.Msg.GetDegraded(), "scaffold must report degraded=true to preserve correctness")
	assert.False(t, resp.Msg.GetFilesystemChanged())
	assert.False(t, resp.Msg.GetProcessesChanged())
	assert.Equal(t, uint32(0), resp.Msg.GetEpochId())
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

	// Bump the epoch to 1.
	_, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
	require.NoError(t, err)

	// Caller asks to reset only if epoch is still 0; should fail-precondition.
	_, err = s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{ExpectedEpochId: 0xdeadbeef}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
}

func TestResetEpochAcceptsZeroExpectedAsForce(t *testing.T) {
	s := newTestService(t)

	// Bump the epoch a few times.
	for range 3 {
		_, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{}))
		require.NoError(t, err)
	}

	// Force reset with expected_epoch_id=0; must succeed regardless.
	resp, err := s.ResetEpoch(context.Background(), connect.NewRequest(&rpc.ResetEpochRequest{ExpectedEpochId: 0}))
	require.NoError(t, err)
	assert.Equal(t, uint32(4), resp.Msg.GetNewEpochId())
}

func TestStatusReportsScaffoldDegradedReason(t *testing.T) {
	s := newTestService(t)

	resp, err := s.Status(context.Background(), connect.NewRequest(&rpc.StatusRequest{}))
	require.NoError(t, err)

	assert.False(t, resp.Msg.GetBpfLoaded())
	assert.False(t, resp.Msg.GetSoftDirtySupported())
	assert.False(t, resp.Msg.GetBtfPresent())
	assert.Equal(t, degradedReasonScaffold, resp.Msg.GetDegradedReason())
}
