package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestSandboxObjectNotFound(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetOrchestratorClient(t, ctx)

	for range 10 {
		_, err := client.Create(ctx, &orchestrator.SandboxCreateRequest{
			Sandbox: &orchestrator.SandboxConfig{
				TemplateId:          "nonexistent-template-id",
				BuildId:             "98d62f9d-9e91-492a-b3e7-03d464778a2e",
				KernelVersion:       "not-needed-here",
				FirecrackerVersion:  "not-needed-here",
				HugePages:           true,
				SandboxId:           "sandbox-with-nonexistent-template-id",
				EnvVars:             nil,
				Metadata:            nil,
				Alias:               nil,
				EnvdVersion:         "",
				Vcpu:                0,
				RamMb:               0,
				TeamId:              "",
				MaxSandboxLength:    0,
				TotalDiskSizeMb:     0,
				Snapshot:            false,
				BaseTemplateId:      "",
				AutoPause:           false,
				EnvdAccessToken:     nil,
				ExecutionId:         "",
				AllowInternetAccess: nil,
				Network:             nil,
			},
			StartTime: timestamppb.Now(),
			EndTime:   timestamppb.Now(),
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok, "err should be a status error")
		if st.Code() == codes.ResourceExhausted {
			t.Log("sandbox creation failed due to resource exhaustion, retrying")
			time.Sleep(time.Second * 5)

			continue
		}

		assert.Equal(t, codes.FailedPrecondition, st.Code(), "status code should be FailedPrecondition")

		return
	}

	t.Log("failed to create sandbox after 10 retries")
	t.FailNow()
}
