package grpcserver

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
