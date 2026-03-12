package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	internalevents "github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// newTestSandbox creates a sandbox for testing. If withSlot is true, the sandbox
// gets a Slot that will fail UpdateInternet (no real network namespace exists).
func newTestSandbox(t *testing.T, withSlot bool) *sandbox.Sandbox {
	t.Helper()

	sbx := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.NewConfig(sandbox.Config{}),
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: id.Generate(),
			},
		},
	}
	sbx.SetStartedAt(time.Now())
	sbx.SetEndAt(time.Now().Add(time.Hour))

	if withSlot {
		slot, err := network.NewSlot("test", 1, network.Config{})
		require.NoError(t, err)
		sbx.Resources = &sandbox.Resources{Slot: slot}
	}

	return sbx
}

func newTestServer(sandboxes ...*sandbox.Sandbox) *Server {
	s := &Server{
		sandboxes:        sandbox.NewSandboxesMap(),
		info:             &service.ServiceInfo{},
		sbxEventsService: internalevents.NewEventsService(nil),
	}
	for _, sbx := range sandboxes {
		s.sandboxes.Insert(sbx)
	}

	return s
}

func TestUpdate_NotFound(t *testing.T) {
	t.Parallel()

	s := newTestServer()
	_, err := s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: "nonexistent",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdate_EndTimeOnly(t *testing.T) {
	t.Parallel()

	sbx := newTestSandbox(t, false)
	s := newTestServer(sbx)

	newEnd := time.Now().Add(2 * time.Hour)
	_, err := s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: sbx.Runtime.SandboxID,
		EndTime:   timestamppb.New(newEnd),
	})

	require.NoError(t, err)
	assert.WithinDuration(t, newEnd, sbx.GetEndAt(), time.Second)
}

func TestUpdate_EgressOnly_FailsAndDoesNotChangeEndTime(t *testing.T) {
	t.Parallel()

	// Sandbox with a Slot but no real namespace — UpdateInternet will fail.
	sbx := newTestSandbox(t, true)
	originalEnd := sbx.GetEndAt()
	s := newTestServer(sbx)

	_, err := s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: sbx.Runtime.SandboxID,
		Egress: &orchestrator.SandboxNetworkEgressConfig{
			DeniedCidrs: []string{"0.0.0.0/0"},
		},
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// end_time was not in the request, so it should be unchanged.
	assert.Equal(t, originalEnd, sbx.GetEndAt())
}

func TestUpdate_EndTimeAndEgress_EgressFails_RevertsEndTime(t *testing.T) {
	t.Parallel()

	// Sandbox with a Slot but no real namespace — UpdateInternet will fail.
	sbx := newTestSandbox(t, true)
	originalEnd := sbx.GetEndAt()
	s := newTestServer(sbx)

	newEnd := time.Now().Add(5 * time.Hour)
	_, err := s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: sbx.Runtime.SandboxID,
		EndTime:   timestamppb.New(newEnd),
		Egress: &orchestrator.SandboxNetworkEgressConfig{
			DeniedCidrs: []string{"0.0.0.0/0"},
		},
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// end_time must be reverted to original since egress failed.
	assert.Equal(t, originalEnd, sbx.GetEndAt())
	// Network egress should not have been set (GetNetwork returns empty defaults).
	egress := sbx.Config.GetNetwork().GetEgress()
	assert.Empty(t, egress.GetAllowedCidrs())
	assert.Empty(t, egress.GetDeniedCidrs())
	assert.Empty(t, egress.GetAllowedDomains())
}

func TestUpdate_NoFieldsSet(t *testing.T) {
	t.Parallel()

	sbx := newTestSandbox(t, false)
	originalEnd := sbx.GetEndAt()
	s := newTestServer(sbx)

	_, err := s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: sbx.Runtime.SandboxID,
	})

	require.NoError(t, err)
	// Nothing should change.
	assert.Equal(t, originalEnd, sbx.GetEndAt())
}
