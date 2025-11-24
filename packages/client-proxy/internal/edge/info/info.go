package info

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ServiceInfo struct {
	NodeID string

	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string
	ServiceStartup       time.Time

	Host string

	terminating bool
	status      api.ClusterNodeStatus
	statusMu    sync.RWMutex
}

func (s *ServiceInfo) GetStatus() api.ClusterNodeStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status
}

func (s *ServiceInfo) SetStatus(ctx context.Context, status api.ClusterNodeStatus) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status != status {
		logger.L().Info(ctx, "Service status changed", zap.String("status", string(status)))
		s.status = status
	}
}

func (s *ServiceInfo) SetTerminating() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.terminating = true
}

func (s *ServiceInfo) IsTerminating() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.terminating
}
