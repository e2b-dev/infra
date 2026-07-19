package dummyserver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestInfoAndDrainStatusShareRuntimeIdentityAndState(t *testing.T) {
	state := NewRuntimeState()
	info := NewInfo("node-1", "service-1", "version", "commit", nil, state)
	sandboxes := NewSandbox("node-1", "service-1", state)

	serviceInfo, err := info.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	drainStatus, err := sandboxes.DrainStatus(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, serviceInfo.GetNodeId(), drainStatus.GetNodeId())
	require.Equal(t, serviceInfo.GetServiceId(), drainStatus.GetServiceId())
	require.True(t, drainStatus.GetAcceptingLifecycleWork())

	_, err = info.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining,
	})
	require.NoError(t, err)
	drainStatus, err = sandboxes.DrainStatus(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.False(t, drainStatus.GetAcceptingLifecycleWork())
	require.True(t, drainStatus.GetDrainFenced())
	require.Greater(t, drainStatus.GetDrainEpoch(), uint64(1))

	_, err = info.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	})
	require.Error(t, err)
}
