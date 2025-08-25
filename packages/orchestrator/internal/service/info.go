package service

import (
	"sync"
	"time"

	"go.uber.org/zap"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type ServiceInfo struct {
	ClientId  string
	ServiceId string

	SourceVersion string
	SourceCommit  string

	Startup time.Time
	Roles   []orchestratorinfo.ServiceInfoRole

	status   orchestratorinfo.ServiceInfoStatus
	statusMu sync.RWMutex
}

var serviceRolesMapper = map[ServiceType]orchestratorinfo.ServiceInfoRole{
	Orchestrator:    orchestratorinfo.ServiceInfoRole_Orchestrator,
	TemplateManager: orchestratorinfo.ServiceInfoRole_TemplateBuilder,
}

func (s *ServiceInfo) GetStatus() orchestratorinfo.ServiceInfoStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status
}

func (s *ServiceInfo) SetStatus(status orchestratorinfo.ServiceInfoStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status != status {
		zap.L().Info("Service status changed", zap.String("status", status.String()))
		s.status = status
	}
}

func NewInfoContainer(clientId string, version string, commit string, instanceID string) *ServiceInfo {
	services := GetServices()
	serviceRoles := make([]orchestratorinfo.ServiceInfoRole, 0)

	for _, service := range services {
		if role, ok := serviceRolesMapper[service]; ok {
			serviceRoles = append(serviceRoles, role)
		}
	}

	serviceInfo := &ServiceInfo{
		ClientId:  clientId,
		ServiceId: instanceID,
		Startup:   time.Now(),
		Roles:     serviceRoles,

		SourceVersion: version,
		SourceCommit:  commit,
	}

	serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Healthy)

	return serviceInfo
}
