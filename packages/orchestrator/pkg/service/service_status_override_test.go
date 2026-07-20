//go:build linux

package service

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type legacyInfoServiceServer struct {
	orchestratorinfo.UnimplementedInfoServiceServer
}

func TestServiceStatusOverrideFencesRuntimeIdentity(t *testing.T) {
	info := &ServiceInfo{ClientId: "node-1", ServiceId: "service-new"}
	server := &Server{info: info}

	_, err := server.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-old",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus().Status)

	_, err = server.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Draining,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-new",
	})
	require.NoError(t, err)
}

func TestPromoteServiceStatusFencedRequiresExactStandbyGeneration(t *testing.T) {
	info := &ServiceInfo{
		ClientId:  "node-1",
		ServiceId: "service-1",
		status: ServiceStatus{
			Status: orchestratorinfo.ServiceInfoStatus_Standby,
		},
		statusEpoch: 4,
	}
	server := &Server{info: info}

	promote := func(epoch uint64) error {
		_, err := server.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
			ExpectedNodeId:      "node-1",
			ExpectedServiceId:   "service-1",
			ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
			ExpectedStatusEpoch: new(epoch),
		})
		return err
	}

	require.Equal(t, codes.FailedPrecondition, status.Code(promote(3)))
	current, epoch, _ := info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, current.Status)
	require.Equal(t, uint64(4), epoch)

	require.NoError(t, promote(4))
	current, epoch, _ = info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, current.Status)
	require.Equal(t, uint64(5), epoch)

	require.Equal(t, codes.FailedPrecondition, status.Code(promote(5)), "repeated promotion must not be a no-op success")
}

func TestLegacyStatusOverridesCannotPromoteStandby(t *testing.T) {
	info := &ServiceInfo{
		ClientId:    "node-1",
		ServiceId:   "service-1",
		status:      ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Standby},
		statusEpoch: 7,
	}
	server := &Server{info: info}

	_, err := server.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus: orchestratorinfo.ServiceInfoStatus_Healthy,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = server.ServiceStatusOverrideFenced(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{
		ServiceStatus:     orchestratorinfo.ServiceInfoStatus_Healthy,
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-1",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	current, epoch, _ := info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, current.Status)
	require.Equal(t, uint64(7), epoch)
}

func TestPromoteServiceStatusFencedRequiresEpochPresence(t *testing.T) {
	info := &ServiceInfo{
		ClientId:  "node-1",
		ServiceId: "service-1",
		status:    ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Standby},
	}
	server := &Server{info: info}

	_, err := server.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-1",
		ExpectedStatus:    orchestratorinfo.ServiceInfoStatus_Standby,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	current, epoch, _ := info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, current.Status)
	require.Zero(t, epoch)
}

func TestPromoteServiceStatusFencedPreservesEpochPresenceOverGRPC(t *testing.T) {
	info := &ServiceInfo{
		ClientId:  "node-1",
		ServiceId: "service-1",
		status:    ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Standby},
	}
	client := newInfoServiceTestClient(t, &Server{info: info})

	request := &orchestratorinfo.ServicePromotionRequest{
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-1",
		ExpectedStatus:    orchestratorinfo.ServiceInfoStatus_Standby,
	}
	_, err := client.PromoteServiceStatusFenced(t.Context(), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	request.ExpectedStatusEpoch = new(uint64(0))
	_, err = client.PromoteServiceStatusFenced(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus().Status)
}

func TestPromoteServiceStatusFencedFailsClosedOnLegacyServer(t *testing.T) {
	client := newInfoServiceTestClient(t, &legacyInfoServiceServer{})

	_, err := client.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
		ExpectedNodeId:      "node-1",
		ExpectedServiceId:   "service-1",
		ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
		ExpectedStatusEpoch: new(uint64(0)),
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestDrainServiceStatusFencedRequiresExactHealthyGeneration(t *testing.T) {
	t.Parallel()

	info := &ServiceInfo{
		ClientId:    "node-1",
		ServiceId:   "service-1",
		status:      ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Healthy},
		statusEpoch: 4,
	}
	server := &Server{info: info}

	drain := func(epoch uint64) error {
		_, err := server.DrainServiceStatusFenced(t.Context(), &orchestratorinfo.ServiceDrainRequest{
			ExpectedNodeId:      "node-1",
			ExpectedServiceId:   "service-1",
			ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Healthy,
			ExpectedStatusEpoch: new(epoch),
		})

		return err
	}

	require.Equal(t, codes.FailedPrecondition, status.Code(drain(3)))
	current, epoch, drainClosed := info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, current.Status)
	require.Equal(t, uint64(4), epoch)
	require.False(t, drainClosed)

	require.NoError(t, drain(4))
	current, epoch, drainClosed = info.GetStatusState()
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, current.Status)
	require.Equal(t, uint64(5), epoch)
	require.True(t, drainClosed)

	require.Equal(t, codes.FailedPrecondition, status.Code(drain(5)), "repeated drain must not be a no-op success")
	_, admitted := info.BeginSandboxLifecycle()
	require.False(t, admitted, "draining must close lifecycle admission")
}

func TestDrainServiceStatusFencedPreservesEpochPresenceOverGRPC(t *testing.T) {
	t.Parallel()

	info := &ServiceInfo{ClientId: "node-1", ServiceId: "service-1", status: ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Healthy}}
	client := newInfoServiceTestClient(t, &Server{info: info})
	request := &orchestratorinfo.ServiceDrainRequest{
		ExpectedNodeId:    "node-1",
		ExpectedServiceId: "service-1",
		ExpectedStatus:    orchestratorinfo.ServiceInfoStatus_Healthy,
	}

	_, err := client.DrainServiceStatusFenced(t.Context(), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus().Status)

	request.ExpectedStatusEpoch = new(uint64(0))
	_, err = client.DrainServiceStatusFenced(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.GetStatus().Status)
}

func TestDrainServiceStatusFencedFailsClosedOnLegacyServer(t *testing.T) {
	t.Parallel()

	client := newInfoServiceTestClient(t, &legacyInfoServiceServer{})

	_, err := client.DrainServiceStatusFenced(t.Context(), &orchestratorinfo.ServiceDrainRequest{
		ExpectedNodeId:      "node-1",
		ExpectedServiceId:   "service-1",
		ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Healthy,
		ExpectedStatusEpoch: new(uint64(0)),
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
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

			info := &ServiceInfo{ClientId: "node-1", ServiceId: "service-1", status: ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Healthy}}
			server := &Server{info: info}
			request := &orchestratorinfo.ServiceDrainRequest{
				ExpectedNodeId:      "node-1",
				ExpectedServiceId:   "service-1",
				ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Healthy,
				ExpectedStatusEpoch: new(uint64(0)),
			}
			mutate(request)

			_, err := server.DrainServiceStatusFenced(t.Context(), request)
			require.Error(t, err)
			require.Equal(t, orchestratorinfo.ServiceInfoStatus_Healthy, info.GetStatus().Status)
		})
	}

	for _, serviceStatus := range []orchestratorinfo.ServiceInfoStatus{
		orchestratorinfo.ServiceInfoStatus_Standby,
		orchestratorinfo.ServiceInfoStatus_Unhealthy,
		orchestratorinfo.ServiceInfoStatus_Draining,
	} {
		t.Run(serviceStatus.String(), func(t *testing.T) {
			t.Parallel()

			info := &ServiceInfo{
				ClientId: "node-1", ServiceId: "service-1",
				status: ServiceStatus{Status: serviceStatus}, statusEpoch: 2,
				drainClosed: serviceStatus == orchestratorinfo.ServiceInfoStatus_Draining,
			}
			server := &Server{info: info}
			_, err := server.DrainServiceStatusFenced(t.Context(), &orchestratorinfo.ServiceDrainRequest{
				ExpectedNodeId: "node-1", ExpectedServiceId: "service-1",
				ExpectedStatus: orchestratorinfo.ServiceInfoStatus_Healthy, ExpectedStatusEpoch: new(uint64(2)),
			})
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
			require.Equal(t, serviceStatus, info.GetStatus().Status)
		})
	}
}

func newInfoServiceTestClient(t *testing.T, server orchestratorinfo.InfoServiceServer) orchestratorinfo.InfoServiceClient {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	orchestratorinfo.RegisterInfoServiceServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(grpcServer.GracefulStop)

	connection, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	return orchestratorinfo.NewInfoServiceClient(connection)
}

func TestPromoteServiceStatusFencedRejectsWrongIdentityAndStatus(t *testing.T) {
	for name, test := range map[string]struct {
		request      *orchestratorinfo.ServicePromotionRequest
		expectedCode codes.Code
	}{
		"missing identity": {
			request: &orchestratorinfo.ServicePromotionRequest{
				ExpectedStatus: orchestratorinfo.ServiceInfoStatus_Standby,
			},
			expectedCode: codes.InvalidArgument,
		},
		"wrong node": {
			request: &orchestratorinfo.ServicePromotionRequest{
				ExpectedNodeId:      "node-old",
				ExpectedServiceId:   "service-1",
				ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
				ExpectedStatusEpoch: new(uint64(0)),
			},
			expectedCode: codes.FailedPrecondition,
		},
		"wrong process": {
			request: &orchestratorinfo.ServicePromotionRequest{
				ExpectedNodeId:      "node-1",
				ExpectedServiceId:   "service-old",
				ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
				ExpectedStatusEpoch: new(uint64(0)),
			},
			expectedCode: codes.FailedPrecondition,
		},
		"wrong expected status": {
			request: &orchestratorinfo.ServicePromotionRequest{
				ExpectedNodeId:    "node-1",
				ExpectedServiceId: "service-1",
				ExpectedStatus:    orchestratorinfo.ServiceInfoStatus_Unhealthy,
			},
			expectedCode: codes.InvalidArgument,
		},
	} {
		t.Run(name, func(t *testing.T) {
			info := &ServiceInfo{
				ClientId:  "node-1",
				ServiceId: "service-1",
				status:    ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Standby},
			}
			server := &Server{info: info}

			_, err := server.PromoteServiceStatusFenced(t.Context(), test.request)
			require.Equal(t, test.expectedCode, status.Code(err))
			require.Equal(t, orchestratorinfo.ServiceInfoStatus_Standby, info.GetStatus().Status)
		})
	}
}

func TestPromoteServiceStatusFencedRejectsUnhealthyAndDraining(t *testing.T) {
	for _, serviceStatus := range []orchestratorinfo.ServiceInfoStatus{
		orchestratorinfo.ServiceInfoStatus_Unhealthy,
		orchestratorinfo.ServiceInfoStatus_Draining,
	} {
		t.Run(serviceStatus.String(), func(t *testing.T) {
			info := &ServiceInfo{
				ClientId:    "node-1",
				ServiceId:   "service-1",
				status:      ServiceStatus{Status: serviceStatus},
				statusEpoch: 2,
				drainClosed: serviceStatus == orchestratorinfo.ServiceInfoStatus_Draining,
			}
			server := &Server{info: info}

			_, err := server.PromoteServiceStatusFenced(t.Context(), &orchestratorinfo.ServicePromotionRequest{
				ExpectedNodeId:      "node-1",
				ExpectedServiceId:   "service-1",
				ExpectedStatus:      orchestratorinfo.ServiceInfoStatus_Standby,
				ExpectedStatusEpoch: new(uint64(2)),
			})
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
			require.Equal(t, serviceStatus, info.GetStatus().Status)
		})
	}
}
