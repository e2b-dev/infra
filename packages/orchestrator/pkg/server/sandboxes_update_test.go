package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	internalevents "github.com/e2b-dev/infra/packages/orchestrator/pkg/events"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

// These tests exercise the rollback path of Update: when egress application
// fails (no real network namespace), end_time and egress must not be changed.

func TestUpdate_EgressOnly_FailsAndDoesNotChangeEndTime(t *testing.T) {
	t.Parallel()

	slot, err := network.NewSlot("test", 1, network.Config{}, network.NoopEgressProxy{})
	require.NoError(t, err)

	sbx := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config:  sandbox.NewConfig(sandbox.Config{}),
			Runtime: sandbox.RuntimeMetadata{SandboxID: id.Generate()},
		},
		Resources: &sandbox.Resources{Slot: slot},
	}
	sbx.SetStartedAt(time.Now())
	sbx.SetEndAt(time.Now().Add(time.Hour))
	originalEnd := sbx.GetEndAt()

	sandboxMap := sandbox.NewSandboxesMap()
	sandboxMap.Insert(context.Background(), sbx)
	sandboxMap.MarkRunning(t.Context(), sbx)

	s := &Server{
		sandboxFactory:   &sandbox.Factory{Sandboxes: sandboxMap},
		info:             &service.ServiceInfo{},
		sbxEventsService: internalevents.NewEventsService(nil),
	}

	_, err = s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: sbx.Runtime.SandboxID,
		Egress: &orchestrator.SandboxNetworkEgressConfig{
			DeniedCidrs: []string{"0.0.0.0/0"},
		},
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Equal(t, originalEnd, sbx.GetEndAt())
}

func TestUpdate_EndTimeAndEgress_EgressFails_RevertsEndTime(t *testing.T) {
	t.Parallel()

	slot, err := network.NewSlot("test", 1, network.Config{}, network.NoopEgressProxy{})
	require.NoError(t, err)

	sbx := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config:  sandbox.NewConfig(sandbox.Config{}),
			Runtime: sandbox.RuntimeMetadata{SandboxID: id.Generate()},
		},
		Resources: &sandbox.Resources{Slot: slot},
	}
	sbx.SetStartedAt(time.Now())
	sbx.SetEndAt(time.Now().Add(time.Hour))
	originalEnd := sbx.GetEndAt()

	sandboxMap := sandbox.NewSandboxesMap()
	sandboxMap.Insert(context.Background(), sbx)
	sandboxMap.MarkRunning(t.Context(), sbx)

	s := &Server{
		sandboxFactory:   &sandbox.Factory{Sandboxes: sandboxMap},
		info:             &service.ServiceInfo{},
		sbxEventsService: internalevents.NewEventsService(nil),
	}

	newEnd := time.Now().Add(5 * time.Hour)
	_, err = s.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
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
	// Network egress should not have been set.
	egress := sbx.Config.GetNetworkEgress()
	assert.Empty(t, egress.GetAllowedCidrs())
	assert.Empty(t, egress.GetDeniedCidrs())
	assert.Empty(t, egress.GetAllowedDomains())
}
