//go:build linux

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// TestPause_FsOnlyVersionGateRefusesBeforeMarkStopping pins the ordering the
// gate's comment calls load-bearing: a filesystem-only pause on a
// non-qualifying Firecracker version must be refused with FailedPrecondition
// BEFORE MarkStopping — a refusal any later leaves a sandbox the store
// already considers stopping, i.e. the stranded-VM failure the gate exists
// to prevent. The proof: after the refused Pause, MarkStopping must still
// succeed, which it cannot for an already-stopping sandbox.
func TestPause_FsOnlyVersionGateRefusesBeforeMarkStopping(t *testing.T) {
	t.Parallel()

	pauseDuration, err := telemetry.GetHistogram(noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/server"), telemetry.PauseDurationHistogramName)
	require.NoError(t, err)

	s := &Server{
		sandboxFactory:       &sandbox.Factory{Sandboxes: sandbox.NewSandboxesMap()},
		sandboxPauseDuration: pauseDuration,
	}

	slot, err := network.NewSlot("test", 1, network.Config{}, network.NoopEgressProxy{})
	require.NoError(t, err)

	const lifecycleID = "lifecycle-1"
	sbx := &sandbox.Sandbox{
		LifecycleID: lifecycleID,
		Metadata: &sandbox.Metadata{
			// A legacy _hash build never qualifies for fs-only snapshots.
			Config: sandbox.NewConfig(sandbox.Config{
				FirecrackerConfig: fc.Config{FirecrackerVersion: "v1.14.1_431f1fc"},
			}),
			Runtime: sandbox.RuntimeMetadata{SandboxID: "sbx-fs-gate"},
		},
		Resources: &sandbox.Resources{Slot: slot},
	}
	s.sandboxFactory.Sandboxes.MarkRunning(t.Context(), sbx)

	_, pauseErr := s.Pause(t.Context(), &orchestrator.SandboxPauseRequest{
		SandboxId:      "sbx-fs-gate",
		FilesystemOnly: true,
	})
	require.Error(t, pauseErr)
	st, ok := status.FromError(pauseErr)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	// The load-bearing half: the refusal happened before MarkStopping, so the
	// sandbox is still fully live — a MarkStopping by a later legitimate
	// lifecycle operation must succeed.
	assert.True(t, s.sandboxFactory.Sandboxes.MarkStopping(t.Context(), "sbx-fs-gate", lifecycleID),
		"a refused fs-only pause must leave the sandbox unmarked; refusing after MarkStopping strands the VM")
}
