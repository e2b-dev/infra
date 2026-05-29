package dummyserver

import (
	"context"
	"runtime"
	"time"

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
}

// NewInfo creates a new InfoServer with a stable per-process serviceID.
func NewInfo(nodeID, serviceID, version, commit string, labels []string) *InfoServer {
	return &InfoServer{
		nodeID:    nodeID,
		serviceID: serviceID,
		version:   version,
		commit:    commit,
		labels:    labels,
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
	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:         s.nodeID,
		ServiceId:      s.serviceID,
		ServiceVersion: s.version,
		ServiceCommit:  s.commit,
		ServiceStatus:  orchestratorinfo.ServiceInfoStatus_Healthy,
		ServiceRoles:   []orchestratorinfo.ServiceInfoRole{orchestratorinfo.ServiceInfoRole_Orchestrator},
		ServiceStartup: timestamppb.New(s.startedAt),
		MachineInfo:    s.machineInfo,
		Labels:         s.labels,
	}, nil
}

func (s *InfoServer) ServiceStatusOverride(_ context.Context, _ *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	// No-op on the dummy server.
	return &emptypb.Empty{}, nil
}
