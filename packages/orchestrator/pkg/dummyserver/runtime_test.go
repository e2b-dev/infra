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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	legacy := &legacyInfoServer{}
	_, err := legacy.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-old",
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.False(t, legacy.mutated)
}

func TestPromoteServiceStatusFencedUsesExactStandbyEpoch(t *testing.T) {
	t.Parallel()

	state := NewRuntimeState()
	require.NoError(t, state.Set(orchestratorinfo.ServiceInfoStatus_Standby))
	_, epoch, _ := state.Get()
	info := NewInfo("node-1", "service-1", "version", "commit", nil, state)

	_, err := info.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
		ExpectedNodeId:      "node-1",
		ExpectedServiceId:   "service-1",
		ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
		ExpectedStatusEpoch: new(epoch - 1),
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = info.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
		ExpectedNodeId:      "node-1",
		ExpectedServiceId:   "service-1",
		ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
		ExpectedStatusEpoch: new(epoch),
	})
	require.NoError(t, err)

	serviceInfo, err := info.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, serviceInfo.GetServiceStatus())
	require.Equal(t, epoch+1, serviceInfo.GetServiceStatusEpoch())
}

func TestDrainServiceStatusFencedUsesExactHealthyEpoch(t *testing.T) {
	t.Parallel()

	state := NewRuntimeState()
	_, epoch, _ := state.Get()
	info := NewInfo("node-1", "service-1", "version", "commit", nil, state)

	request := &orchestratorinfo.ServiceDrainRequest{
		ExpectedNodeId: "node-1", ExpectedServiceId: "service-1",
		ExpectedStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	}
	_, err := info.DrainServiceStatusFenced(t.Context(), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	request.ExpectedStatusEpoch = new(epoch - 1)
	_, err = info.DrainServiceStatusFenced(t.Context(), request)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	request.ExpectedStatusEpoch = new(epoch)
	_, err = info.DrainServiceStatusFenced(t.Context(), request)
	require.NoError(t, err)

	serviceInfo, err := info.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, serviceInfo.GetServiceStatus())
	require.Equal(t, epoch+1, serviceInfo.GetServiceStatusEpoch())
	_, admitted := state.BeginLifecycle()
	require.False(t, admitted)

	_, err = info.DrainServiceStatusFenced(t.Context(), request)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestDrainServiceStatusFencedRejectsWrongIdentityAndStatus(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*orchestratorinfo.ServiceDrainRequest){
		"missing identity": func(request *orchestratorinfo.ServiceDrainRequest) { request.ExpectedNodeId = "" },
		"wrong node":       func(request *orchestratorinfo.ServiceDrainRequest) { request.ExpectedNodeId = "node-old" },
		"wrong process":    func(request *orchestratorinfo.ServiceDrainRequest) { request.ExpectedServiceId = "service-old" },
		"wrong status": func(request *orchestratorinfo.ServiceDrainRequest) {
			request.ExpectedStatus = orchestratorinfo.ServiceInfoStatus_Standby
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			state := NewRuntimeState()
			_, epoch, _ := state.Get()
			info := NewInfo("node-1", "service-1", "version", "commit", nil, state)
			request := &orchestratorinfo.ServiceDrainRequest{
				ExpectedNodeId: "node-1", ExpectedServiceId: "service-1",
				ExpectedStatus: orchestratorinfo.ServiceInfoStatus_Healthy, ExpectedStatusEpoch: new(epoch),
			}
			mutate(request)

			_, err := info.DrainServiceStatusFenced(t.Context(), request)
			require.Error(t, err)
			currentStatus, currentEpoch, drained := state.Get()
			require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, currentStatus)
			require.Equal(t, epoch, currentEpoch)
			require.False(t, drained)
		})
	}
}

func TestDrainServiceStatusFencedFailsClosedOnLegacyServer(t *testing.T) {
	t.Parallel()

	legacy := &legacyInfoServer{}
	_, err := legacy.DrainServiceStatusFenced(t.Context(), &orchestratorinfo.ServiceDrainRequest{
		ExpectedNodeId:      "node-1",
		ExpectedServiceId:   "service-1",
		ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Healthy,
		ExpectedStatusEpoch: new(uint64(0)),
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.False(t, legacy.mutated)
}

func TestDummyLegacyStatusOverridesCannotPromoteStandby(t *testing.T) {
	t.Parallel()

	state := NewRuntimeState()
	require.NoError(t, state.Set(orchestratorinfo.ServiceInfoStatus_Standby))
	_, epoch, _ := state.Get()
	info := NewInfo("node-1", "service-1", "version", "commit", nil, state)

	_, err := info.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = info.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Healthy,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-1",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	currentStatus, currentEpoch, _ := state.Get()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, currentStatus)
	require.Equal(t, epoch, currentEpoch)
}

func TestStandbyRuntimeRejectsLifecycleWorkUntilPromoted(t *testing.T) {
	t.Parallel()

	state := NewRuntimeStateWithStatus(orchestratorinfo.ServiceInfoStatus_Standby)
	statusBefore, epoch, _ := state.Get()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, statusBefore)

	release, admitted := state.BeginLifecycle()
	require.False(t, admitted)
	require.Nil(t, release)
	require.NoError(t, state.PromoteStandby(epoch))

	release, admitted = state.BeginLifecycle()
	require.True(t, admitted)
	require.NotNil(t, release)
	release()
}
