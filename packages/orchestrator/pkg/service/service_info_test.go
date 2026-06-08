//go:build linux

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type fakeDrainController struct {
	starts atomic.Int64
	forces chan struct{}
}

func (f *fakeDrainController) StartDraining(context.Context) {
	f.starts.Add(1)
}

func (f *fakeDrainController) ForceStop(context.Context) error {
	f.forces <- struct{}{}

	return nil
}

func newTestInfoServer(controller DrainController) *Server {
	return NewInfoService(
		NewInfoContainer(context.Background(), "node-id", "version", "commit", "service-id", machineinfo.MachineInfo{}, cfg.Config{}),
		nil,
		nil,
		controller,
	)
}

func TestServiceStatusOverrideDrainingStartsDrain(t *testing.T) {
	t.Parallel()

	controller := &fakeDrainController{forces: make(chan struct{}, 1)}
	s := newTestInfoServer(controller)

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, s.info.GetStatus())
}

func TestServiceStatusOverrideForcedDrainingStartsForceStop(t *testing.T) {
	t.Parallel()

	forceStop := true
	controller := &fakeDrainController{forces: make(chan struct{}, 1)}
	s := newTestInfoServer(controller)

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
		ForceStop:     &forceStop,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())

	select {
	case <-controller.forces:
	case <-time.After(time.Second):
		t.Fatal("forced drain did not start")
	}
}

func TestServiceStatusOverrideRejectsForceStopWithoutDraining(t *testing.T) {
	t.Parallel()

	forceStop := true
	s := newTestInfoServer(&fakeDrainController{forces: make(chan struct{}, 1)})

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Unhealthy,
		ForceStop:     &forceStop,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServiceStatusOverrideRejectsDrainReactivation(t *testing.T) {
	t.Parallel()

	s := newTestInfoServer(&fakeDrainController{forces: make(chan struct{}, 1)})

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)

	_, err = s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
