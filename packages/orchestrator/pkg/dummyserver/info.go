package dummyserver

import (
	"context"
	"runtime"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

// InfoServer is a static implementation of orchestratorinfo.InfoServiceServer
// returning the bare minimum the api needs to route to this orchestrator.
type InfoServer struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	nodeID      string
	serviceID   string
	version     string
	commit      string
	labels      []string
	startedAt   time.Time
	machineInfo *orchestratorinfo.MachineInfo
	state       *RuntimeState
}

// NewInfo creates a new InfoServer with a stable per-process serviceID.
func NewInfo(nodeID, serviceID, version, commit string, labels []string, state *RuntimeState) *InfoServer {
	return &InfoServer{
		nodeID:    nodeID,
		serviceID: serviceID,
		version:   version,
		commit:    commit,
		labels:    labels,
		state:     state,
		startedAt: time.Now(),
		machineInfo: &orchestratorinfo.MachineInfo{
			CpuArchitecture: runtime.GOARCH,
			CpuFamily:       "darwin-dummy",
			CpuModel:        "darwin-dummy",
			CpuModelName:    "darwin-dummy",
		},
	}
}

func (s *InfoServer) ServiceInfo(_ context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	serviceStatus, serviceStatusEpoch, _ := s.state.Get()

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:             s.nodeID,
		ServiceId:          s.serviceID,
		ServiceVersion:     s.version,
		ServiceCommit:      s.commit,
		ServiceStatus:      serviceStatus,
		ServiceStatusEpoch: serviceStatusEpoch,
		ServiceRoles:       []orchestratorinfo.ServiceInfoRole{orchestratorinfo.ServiceInfoRole_Orchestrator},
		ServiceStartup:     timestamppb.New(s.startedAt),
		MachineInfo:        s.machineInfo,
		Labels:             s.labels,
	}, nil
}

func (s *InfoServer) PromoteServiceStatusFenced(_ context.Context, request *orchestratorinfo.ServicePromotionRequest) (*emptypb.Empty, error) {
	if request.GetExpectedNodeId() == "" || request.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}
	if request.GetExpectedStatus() != orchestratorinfo.ServiceInfoStatus_Standby {
		return nil, status.Error(codes.InvalidArgument, "expected service status must be Standby")
	}
	if request.ExpectedStatusEpoch == nil {
		return nil, status.Error(codes.InvalidArgument, "expected service status epoch is required")
	}
	if request.GetExpectedNodeId() != s.nodeID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if request.GetExpectedServiceId() != s.serviceID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if err := s.state.PromoteStandby(request.GetExpectedStatusEpoch()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	return &emptypb.Empty{}, nil
}

func (s *InfoServer) DrainServiceStatusFenced(_ context.Context, request *orchestratorinfo.ServiceDrainRequest) (*emptypb.Empty, error) {
	if request.GetExpectedNodeId() == "" || request.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}
	if request.GetExpectedStatus() != orchestratorinfo.ServiceInfoStatus_Healthy {
		return nil, status.Error(codes.InvalidArgument, "expected service status must be Healthy")
	}
	if request.ExpectedStatusEpoch == nil {
		return nil, status.Error(codes.InvalidArgument, "expected service status epoch is required")
	}
	if request.GetExpectedNodeId() != s.nodeID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if request.GetExpectedServiceId() != s.serviceID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if err := s.state.DrainHealthy(request.GetExpectedStatusEpoch()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	return &emptypb.Empty{}, nil
}

func (s *InfoServer) ServiceStatusOverride(_ context.Context, request *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	return s.applyServiceStatusOverride(request)
}

func (s *InfoServer) ServiceStatusOverrideFenced(_ context.Context, request *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	if expected := request.GetExpectedNodeId(); expected != "" && expected != s.nodeID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if expected := request.GetExpectedServiceId(); expected != "" && expected != s.serviceID {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if request.GetExpectedNodeId() == "" || request.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}

	return s.applyServiceStatusOverride(request)
}

func (s *InfoServer) applyServiceStatusOverride(request *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	if err := s.state.Set(request.GetServiceStatus()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	return &emptypb.Empty{}, nil
}
