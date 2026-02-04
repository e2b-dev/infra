package internal

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ServiceHealth string

const (
	Healthy   ServiceHealth = "healthy"
	Draining  ServiceHealth = "draining"
	Unhealthy ServiceHealth = "unhealthy"
)

type ServiceInfo struct {
	status   ServiceHealth
	statusMu sync.RWMutex
}

func (s *ServiceInfo) GetStatus() ServiceHealth {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	return s.status
}

func (s *ServiceInfo) SetStatus(ctx context.Context, status ServiceHealth) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status != status {
		logger.L().Info(ctx, "Service status changed", zap.String("status", string(status)))
		s.status = status
	}
}
