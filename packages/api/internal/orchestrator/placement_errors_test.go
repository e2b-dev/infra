package orchestrator

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
)

func TestPlacementAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		wantCode      int
		wantErrorCode string
		wantMsg       string
	}{
		{
			name:          "timeout",
			err:           placement.PlacementTimeoutError{Attempts: 2},
			wantCode:      http.StatusGatewayTimeout,
			wantErrorCode: "sandbox_placement_timeout",
			wantMsg:       "Failed to place sandbox: placement timed out after 2 attempt(s), please retry",
		},
		{
			name:          "zero-attempt timeout has no counter",
			err:           placement.PlacementTimeoutError{Attempts: 0},
			wantCode:      http.StatusGatewayTimeout,
			wantErrorCode: "sandbox_placement_timeout",
			wantMsg:       "Failed to place sandbox: placement timed out, please retry",
		},
		{
			name:          "no nodes available",
			err:           placement.NoNodesAvailableError{},
			wantCode:      http.StatusServiceUnavailable,
			wantErrorCode: "sandbox_capacity_unavailable",
			wantMsg:       "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly",
		},
		{
			name:          "no eligible node",
			err:           placement.FailedToPlaceSandboxError{},
			wantCode:      http.StatusServiceUnavailable,
			wantErrorCode: "sandbox_no_compatible_node",
			wantMsg:       "Failed to place sandbox: no compatible node for this template's requirements",
		},
		{
			// Distinct from "no eligible node": the cluster is not busy, it is
			// too old, and the client must not be told to retry.
			name:          "feature unsupported by the cluster",
			err:           placement.UnsupportedFeatureError{Features: []string{"https-backend-ports"}, MinVersion: "0.11.0"},
			wantCode:      http.StatusServiceUnavailable,
			wantErrorCode: "sandbox_feature_unsupported",
			wantMsg:       "Failed to place sandbox: no available orchestrator at version 0.11.0 or above for a feature the request asked for",
		},
		{
			name:          "create failed forwards safe status message",
			err:           placement.SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.FailedPrecondition, "sandbox files for 'abc' not found")},
			wantCode:      http.StatusInternalServerError,
			wantErrorCode: "sandbox_create_failed",
			wantMsg:       "Failed to place sandbox: sandbox files for 'abc' not found",
		},
		{
			name:          "create failed forwards invalid-argument message",
			err:           placement.SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.InvalidArgument, "invalid volume mount path")},
			wantCode:      http.StatusInternalServerError,
			wantErrorCode: "sandbox_create_failed",
			wantMsg:       "Failed to place sandbox: invalid volume mount path",
		},
		{
			name:          "create failed hides unsafe status message",
			err:           placement.SandboxCreateError{Attempts: 3, LastErr: status.Error(codes.Internal, "node ip-10-0-0-1: disk full")},
			wantCode:      http.StatusInternalServerError,
			wantErrorCode: "sandbox_create_failed",
			wantMsg:       "Failed to place sandbox: sandbox creation failed on 3 node(s), please retry; if the problem persists, contact us",
		},
		{
			name:          "create failed hides non-grpc error",
			err:           placement.SandboxCreateError{Attempts: 2, LastErr: errors.New("dial tcp 10.0.0.1: connection refused")},
			wantCode:      http.StatusInternalServerError,
			wantErrorCode: "sandbox_create_failed",
			wantMsg:       "Failed to place sandbox: sandbox creation failed on 2 node(s), please retry; if the problem persists, contact us",
		},
		{
			name:          "wrapped typed error still classified",
			err:           fmt.Errorf("failed to place sandbox: %w", placement.NoNodesAvailableError{}),
			wantCode:      http.StatusServiceUnavailable,
			wantErrorCode: "sandbox_capacity_unavailable",
			wantMsg:       "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly",
		},
		{
			name:          "unknown error keeps generic message",
			err:           errors.New("boom"),
			wantCode:      http.StatusInternalServerError,
			wantErrorCode: "internal_server_error",
			wantMsg:       "Failed to place sandbox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			apiErr := placementAPIError(tt.err)

			require.NotNil(t, apiErr)
			assert.Equal(t, tt.wantCode, apiErr.Code)
			assert.Equal(t, tt.wantErrorCode, apiErr.ErrorCode)
			assert.Equal(t, tt.wantMsg, apiErr.ClientMsg)
			assert.ErrorContains(t, apiErr.Err, tt.err.Error())
		})
	}
}
