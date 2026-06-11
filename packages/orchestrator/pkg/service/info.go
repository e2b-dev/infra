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

	status          orchestratorinfo.ServiceInfoStatus
	statusChangedAt time.Time
	statusMu        sync.RWMutex
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

// GetStatusAndChangedAt atomically returns the status together with the timestamp of the last status change.
func (s *ServiceInfo) GetStatusAndChangedAt() (orchestratorinfo.ServiceInfoStatus, time.Time) {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status, s.statusChangedAt
}

func (s *ServiceInfo) SetStatus(ctx context.Context, status orchestratorinfo.ServiceInfoStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status != status {
		logger.L().Info(ctx, "Service status changed", zap.String("status", status.String()))
		s.status = status
		s.statusChangedAt = time.Now()
	}
}

func NewInfoContainer(ctx context.Context, clientId string, version string, commit string, instanceID string, machineInfo machineinfo.MachineInfo, config cfg.Config) *ServiceInfo {
	services := cfg.GetServices(config)
	serviceRoles := make([]orchestratorinfo.ServiceInfoRole, 0)

	for _, service := range services {
		if role, ok := serviceRolesMapper[service]; ok {
			serviceRoles = append(serviceRoles, role)
		}
	}

	startup := time.Now()
	serviceInfo := &ServiceInfo{
		ClientId:  clientId,
		ServiceId: instanceID,

		statusChangedAt: startup,

		Startup:     startup,
		Roles:       serviceRoles,
		Labels:      config.NodeLabels,
		MachineInfo: machineInfo,

		SourceVersion: version,
		SourceCommit:  commit,
	}

	serviceInfo.SetStatus(ctx, orchestratorinfo.ServiceInfoStatus_Healthy)

	return serviceInfo
}
