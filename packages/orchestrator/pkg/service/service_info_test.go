//go:build linux

package service

import (
	"context"
	"errors"
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
	starts    atomic.Int64
	forces    atomic.Int64
	onStart   func()
	forceStop func(context.Context) error
}

func (f *fakeDrainController) StartDraining(context.Context) {
	if f.onStart != nil {
		f.onStart()
	}

	f.starts.Add(1)
}

func (f *fakeDrainController) ForceStop(ctx context.Context) error {
	f.forces.Add(1)
	if f.forceStop != nil {
		return f.forceStop(ctx)
	}

	return nil
}

func newTestInfoServer(controller DrainController) *Server {
	info := NewInfoContainer("node-id", "version", "commit", "service-id", machineinfo.MachineInfo{}, cfg.Config{})

	return NewInfoService(
		info,
		nil,
		nil,
		NewDrainCoordinator(info, controller),
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
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, s.info.GetStatus().Status)
}

func TestServiceStatusOverrideStartsDrainBeforePublishingStatus(t *testing.T) {
	t.Parallel()

	info := NewInfoContainer("node-id", "version", "commit", "service-id", machineinfo.MachineInfo{}, cfg.Config{})
	controller := &fakeDrainController{
		onStart: func() {
			require.NotEqual(t, orchestratorinfo.ServiceInfoStatus_Draining, info.status.Status)
		},
	}
	s := NewInfoService(info, nil, nil, NewDrainCoordinator(info, controller))

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), controller.starts.Load())
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.GetStatus().Status)
}

func TestBeginDrainingClosesAdmissionWithoutOverridingStatus(t *testing.T) {
	t.Parallel()

	info := NewInfoContainer("node-id", "version", "commit", "service-id", machineinfo.MachineInfo{}, cfg.Config{})
	info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Unhealthy)
	controller := &fakeDrainController{}
	drainCoordinator := NewDrainCoordinator(info, controller)

	transitioned, err := drainCoordinator.BeginDraining(
		t.Context(),
		nil,
		orchestratorinfo.ServiceInfoStatus_Healthy,
		orchestratorinfo.ServiceInfoStatus_Standby,
	)

	require.NoError(t, err)
	require.False(t, transitioned)
	require.Equal(t, int64(1), controller.starts.Load())
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Unhealthy, info.GetStatus().Status)
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

	const concurrentDrainIterations = 100

	for range concurrentDrainIterations {
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
		require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, s.info.GetStatus().Status)
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

func TestServiceStatusOverrideForceStopModeRetriesAfterFailure(t *testing.T) {
	t.Parallel()

	forceStop := true
	secondAttempt := make(chan struct{})
	var attempts atomic.Int64
	controller := &fakeDrainController{
		forceStop: func(context.Context) error {
			attempt := attempts.Add(1)
			if attempt == 1 {
				return errors.New("force stop failed")
			}
			if attempt == 2 {
				close(secondAttempt)
			}

			return nil
		},
	}
	s := newTestInfoServer(controller)
	s.forceStopRetryDelay = 10 * time.Millisecond

	_, err := s.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
		ForceStop:     &forceStop,
	})
	require.NoError(t, err)

	select {
	case <-secondAttempt:
	case <-time.After(time.Second):
		t.Fatal("forced drain did not retry after failure")
	}
	require.Equal(t, int64(2), controller.forces.Load())
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
