package dummyserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type legacyInfoServer struct {
	orchestratorinfo.UnimplementedInfoServiceServer
	mutated bool
}

func (s *legacyInfoServer) ServiceStatusOverride(context.Context, *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	s.mutated = true
	return &emptypb.Empty{}, nil
}

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

func TestStatusOverrideFencesRuntimeIdentity(t *testing.T) {
	state := NewRuntimeState()
	info := NewInfo("node-1", "service-new", "version", "commit", nil, state)

	_, err := info.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-old",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	serviceInfo, infoErr := info.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, infoErr)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, serviceInfo.GetServiceStatus())

	_, err = info.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-new",
	})
	require.NoError(t, err)
}

func TestFencedStatusOverrideFailsClosedOnLegacyServer(t *testing.T) {
	legacy := &legacyInfoServer{}
	_, err := legacy.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-old",
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.False(t, legacy.mutated)
}
