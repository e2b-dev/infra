package info

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type ServiceInfo struct {
	NodeId    string
	ServiceId string

	SourceVersion string
	SourceCommit  string

	Startup time.Time
	Host    string

	status   api.ClusterNodeStatus
	statusMu sync.RWMutex
}

func (s *ServiceInfo) GetStatus() api.ClusterNodeStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status
}

func (s *ServiceInfo) SetStatus(status api.ClusterNodeStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status != status {
		zap.L().Info("Service status changed", zap.String("status", string(status)))
		s.status = status
	}
}
