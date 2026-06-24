package nodemanager

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// mockSandboxListClient implements orchestrator.SandboxServiceClient and returns
// a canned List response, so GetSandboxes' proto->Sandbox reconstruction can be
// tested without a live orchestrator.
type mockSandboxListClient struct {
	orchestrator.SandboxServiceClient

	resp *orchestrator.SandboxListResponse
}

func (m *mockSandboxListClient) List(_ context.Context, _ *emptypb.Empty, _ ...grpc.CallOption) (*orchestrator.SandboxListResponse, error) {
	return m.resp, nil
}

// TestGetSandboxes_RestoresAutoPauseFilesystemOnly verifies that the auto-pause
// snapshot-kind policy round-trips through the orchestrator's SandboxConfig when
// the API re-syncs its sandbox list (e.g. after a restart). The proto field
// exists for exactly this path, so without it the policy would silently revert
// to a memory auto-pause.
func TestGetSandboxes_RestoresAutoPauseFilesystemOnly(t *testing.T) {
	t.Parallel()

	now := time.Now()
	runningSandbox := func(id string, autoPauseFilesystemOnly bool) *orchestrator.RunningSandbox {
		return &orchestrator.RunningSandbox{
			StartTime: timestamppb.New(now),
			EndTime:   timestamppb.New(now.Add(time.Hour)),
			Config: &orchestrator.SandboxConfig{
				SandboxId:               id,
				TemplateId:              "tmpl",
				BaseTemplateId:          "tmpl",
				TeamId:                  uuid.NewString(),
				BuildId:                 uuid.NewString(),
				ExecutionId:             uuid.NewString(),
				AutoPause:               true,
				AutoPauseFilesystemOnly: autoPauseFilesystemOnly,
			},
		}
	}

	node := NewTestNode("test-node", api.NodeStatusReady, 0, 4)
	node.SetSandboxClient(&mockSandboxListClient{
		resp: &orchestrator.SandboxListResponse{
			Sandboxes: []*orchestrator.RunningSandbox{
				runningSandbox("fs-only", true),
				runningSandbox("memory", false),
			},
		},
	})

	sandboxes, err := node.GetSandboxes(t.Context())
	require.NoError(t, err)
	require.Len(t, sandboxes, 2)

	got := make(map[string]bool, len(sandboxes))
	for _, sbx := range sandboxes {
		got[sbx.SandboxID] = sbx.AutoPauseFilesystemOnly
	}

	assert.True(t, got["fs-only"], "filesystem-only auto-pause policy must survive an orchestrator re-sync")
	assert.False(t, got["memory"], "memory auto-pause policy must survive an orchestrator re-sync")
}
