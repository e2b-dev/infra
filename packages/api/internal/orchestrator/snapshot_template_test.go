package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	grpcorchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestCheckpointFailureState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		wantState grpcorchestrator.SandboxCheckpointSandboxState
		wantErr   string
	}{
		{
			name:      "context deadline exceeded",
			err:       context.DeadlineExceeded,
			wantState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING,
			wantErr:   "checkpoint failed",
		},
		{
			name:      "grpc canceled",
			err:       grpcstatus.Error(codes.Canceled, "canceled"),
			wantState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING,
			wantErr:   "checkpoint failed",
		},
		{
			name:      "grpc internal",
			err:       grpcstatus.Error(codes.Internal, "snapshot failed"),
			wantState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_KILLED,
			wantErr:   "checkpoint failed",
		},
		{
			name: "failure detail running",
			err: checkpointFailureStatus(t, &grpcorchestrator.SandboxCheckpointFailure{
				SandboxState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING,
				ErrorMessage: "upload failed",
			}),
			wantState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_RUNNING,
			wantErr:   "upload failed",
		},
		{
			name: "failure detail killed",
			err: checkpointFailureStatus(t, &grpcorchestrator.SandboxCheckpointFailure{
				SandboxState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_KILLED,
				ErrorMessage: "snapshot failed",
			}),
			wantState: grpcorchestrator.SandboxCheckpointSandboxState_SANDBOX_CHECKPOINT_SANDBOX_STATE_KILLED,
			wantErr:   "snapshot failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotState, gotErr := checkpointFailureState(tt.err)
			assert.Equal(t, tt.wantState, gotState)
			assert.ErrorContains(t, gotErr, tt.wantErr)
		})
	}
}

func checkpointFailureStatus(t *testing.T, failure *grpcorchestrator.SandboxCheckpointFailure) error {
	t.Helper()

	st, err := grpcstatus.New(codes.Internal, "checkpoint failed").WithDetails(failure)
	require.NoError(t, err)

	return st.Err()
}
