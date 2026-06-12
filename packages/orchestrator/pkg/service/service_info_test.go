//go:build linux

package service

import (
	"context"
	"sync"
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
	forces atomic.Int64
}

func (f *fakeDrainController) StartDraining(context.Context) {
	f.starts.Add(1)
}

func (f *fakeDrainController) ForceStop(context.Context) error {
	f.forces.Add(1)

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

	controller := &fakeDrainController{}
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
	controller := &fakeDrainController{}
	s := newTestInfoServer(controller)

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
		ForceStop:     &forceStop,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())

	require.Eventually(t, func() bool {
		return controller.forces.Load() == 1
	}, time.Second, 10*time.Millisecond, "forced drain did not start")
}

func TestServiceStatusOverrideRejectsForceStopWithoutDraining(t *testing.T) {
	t.Parallel()

	forceStop := true
	s := newTestInfoServer(&fakeDrainController{})

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Unhealthy,
		ForceStop:     &forceStop,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServiceStatusOverrideRejectsDrainReactivation(t *testing.T) {
	t.Parallel()

	s := newTestInfoServer(&fakeDrainController{})

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)

	_, err = s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestServiceStatusOverrideConcurrentDrainCannotFinishHealthy(t *testing.T) {
	t.Parallel()

	for range 1000 {
		s := newTestInfoServer(nil)
		start := make(chan struct{})
		drainDone := make(chan error, 1)
		healthyDone := make(chan error, 1)

		go func() {
			<-start
			_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
				ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
			})
			drainDone <- err
		}()

		go func() {
			<-start
			_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
				ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
			})
			healthyDone <- err
		}()

		close(start)
		drainErr := <-drainDone
		healthyErr := <-healthyDone
		require.NoError(t, drainErr)
		if healthyErr != nil {
			require.Equal(t, codes.FailedPrecondition, status.Code(healthyErr))
		}
		require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, s.info.GetStatus())
	}
}

func TestServiceStatusOverrideForceStopOnlyOnce(t *testing.T) {
	t.Parallel()

	forceStop := true
	for _, tc := range []struct {
		name       string
		concurrent bool
	}{
		{name: "sequential"},
		{name: "concurrent", concurrent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			controller := &fakeDrainController{}
			s := newTestInfoServer(controller)
			req := &orchestratorinfo.ServiceStatusChangeRequest{
				ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
				ForceStop:     &forceStop,
			}

			if tc.concurrent {
				start := make(chan struct{})
				errs := make(chan error, 2)
				var wg sync.WaitGroup
				for range 2 {
					wg.Go(func() {
						<-start
						_, err := s.ServiceStatusOverride(t.Context(), req)
						errs <- err
					})
				}
				close(start)
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
			} else {
				_, err := s.ServiceStatusOverride(t.Context(), req)
				require.NoError(t, err)
				_, err = s.ServiceStatusOverride(t.Context(), req)
				require.NoError(t, err)
			}

			require.Equal(t, int64(1), controller.starts.Load())
			require.Eventually(t, func() bool {
				return controller.forces.Load() == 1
			}, time.Second, 10*time.Millisecond, "forced drain should start once")
		})
	}
}

func TestServiceStatusOverrideDrainThenForceStopEscalates(t *testing.T) {
	t.Parallel()

	forceStop := true
	controller := &fakeDrainController{}
	s := newTestInfoServer(controller)

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())
	require.Zero(t, controller.forces.Load())

	_, err = s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
		ForceStop:     &forceStop,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())
	require.Eventually(t, func() bool {
		return controller.forces.Load() == 1
	}, time.Second, 10*time.Millisecond, "forced drain did not start")
}
