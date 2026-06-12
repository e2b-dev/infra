//go:build linux

package service

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ServiceInfo struct {
	ClientId  string
	ServiceId string

	SourceVersion string
	SourceCommit  string

	Startup     time.Time
	Roles       []orchestratorinfo.ServiceInfoRole
	Labels      []string
	MachineInfo machineinfo.MachineInfo

	status   orchestratorinfo.ServiceInfoStatus
	statusMu sync.RWMutex
}

var serviceRolesMapper = map[cfg.ServiceType]orchestratorinfo.ServiceInfoRole{
	cfg.Orchestrator:    orchestratorinfo.ServiceInfoRole_Orchestrator,
	cfg.TemplateManager: orchestratorinfo.ServiceInfoRole_TemplateBuilder,
}

func (s *ServiceInfo) GetStatus() orchestratorinfo.ServiceInfoStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status
}

func (s *ServiceInfo) SetStatus(ctx context.Context, status orchestratorinfo.ServiceInfoStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	s.setStatusLocked(ctx, status)
}

func (s *ServiceInfo) TransitionStatus(ctx context.Context, status orchestratorinfo.ServiceInfoStatus, validate func(current orchestratorinfo.ServiceInfoStatus) error) (bool, error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if validate != nil {
		if err := validate(s.status); err != nil {
			return false, err
		}
	}

	return s.setStatusLocked(ctx, status), nil
}

func (s *ServiceInfo) setStatusLocked(ctx context.Context, status orchestratorinfo.ServiceInfoStatus) bool {
	if s.status != status {
		logger.L().Info(ctx, "Service status changed", zap.String("status", status.String()))
		s.status = status

		return true
	}

	return false
}

func NewInfoContainer(ctx context.Context, clientId string, version string, commit string, instanceID string, machineInfo machineinfo.MachineInfo, config cfg.Config) *ServiceInfo {
	services := cfg.GetServices(config)
	serviceRoles := make([]orchestratorinfo.ServiceInfoRole, 0)

	for _, service := range services {
		if role, ok := serviceRolesMapper[service]; ok {
			serviceRoles = append(serviceRoles, role)
		}
	}

	serviceInfo := &ServiceInfo{
		ClientId:  clientId,
		ServiceId: instanceID,

		Startup:     time.Now(),
		Roles:       serviceRoles,
		Labels:      config.NodeLabels,
		MachineInfo: machineInfo,

		SourceVersion: version,
		SourceCommit:  commit,
	}

	serviceInfo.SetStatus(ctx, orchestratorinfo.ServiceInfoStatus_Healthy)

	return serviceInfo
}
