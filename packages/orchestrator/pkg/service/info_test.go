//go:build linux

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestNewInfoContainerCanStartStandby(t *testing.T) {
	info := NewInfoContainer("node-1", "version", "commit", "service-1", machineinfo.MachineInfo{}, cfg.Config{StartStandby: true})

	status, epoch, drainClosed := info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, status.Status)
	require.Equal(t, uint64(0), epoch)
	require.False(t, drainClosed)
	_, admitted := info.BeginSandboxLifecycle()
	require.False(t, admitted)
}

func TestNewInfoContainerStartsHealthyByDefault(t *testing.T) {
	info := NewInfoContainer("node-1", "version", "commit", "service-1", machineinfo.MachineInfo{}, cfg.Config{})

	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus().Status)
	release, admitted := info.BeginSandboxLifecycle()
	require.True(t, admitted)
	release()
}

func TestDrainClosesAdmissionBeforeLifecycleWorkFinishes(t *testing.T) {
	info := &ServiceInfo{}
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Healthy))

	release, admitted := info.BeginSandboxLifecycle()
	require.True(t, admitted)
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining))

	if _, admitted := info.BeginSandboxLifecycle(); admitted {
		t.Fatal("draining service admitted a sandbox create")
	}
	waitCtx, cancelWait := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelWait()
	require.ErrorIs(t, info.WaitForSandboxLifecycles(waitCtx), context.DeadlineExceeded)

	release()
	require.NoError(t, info.WaitForSandboxLifecycles(t.Context()))
}

func TestDrainWaitCanBeCancelledWithoutReopeningAdmission(t *testing.T) {
	info := &ServiceInfo{}
	if err := info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Healthy); err != nil {
		t.Fatal(err)
	}

	release, admitted := info.BeginSandboxLifecycle()
	if !admitted {
		t.Fatal("healthy service rejected lifecycle work")
	}
	defer release()

	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.True(t, errors.Is(info.WaitForSandboxLifecycles(ctx), context.Canceled))
	if _, admitted := info.BeginSandboxLifecycle(); admitted {
		t.Fatal("canceled drain wait reopened lifecycle admission")
	}
}

func TestDrainingServiceCannotBeReenabled(t *testing.T) {
	info := &ServiceInfo{}
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Healthy))
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining))
	_, drainEpoch, drainFenced := info.GetDrainState()
	require.True(t, drainFenced)
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Unhealthy))

	require.ErrorIs(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Healthy), ErrDrainingServiceCannotBeReenabled)
	status, currentEpoch, drainFenced := info.GetDrainState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Unhealthy, status)
	require.Greater(t, currentEpoch, drainEpoch)
	require.True(t, drainFenced)
}

func TestDrainingFencesPreviouslyUnhealthyService(t *testing.T) {
	info := &ServiceInfo{}
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Unhealthy))
	require.NoError(t, info.SetStatus(t.Context(), orchestratorinfo.ServiceInfoStatus_Draining))

	status, _, drainFenced := info.GetDrainState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, status)
	require.True(t, drainFenced)
}
