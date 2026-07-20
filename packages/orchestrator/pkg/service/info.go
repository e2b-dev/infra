//go:build linux

package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// ServiceStatus bundles the service status with the time of its last change.
type ServiceStatus struct {
	Status    orchestratorinfo.ServiceInfoStatus
	ChangedAt time.Time
}

type ServiceInfo struct {
	ClientId  string
	ServiceId string

	SourceVersion string
	SourceCommit  string

	Startup     time.Time
	Roles       []orchestratorinfo.ServiceInfoRole
	Labels      []string
	MachineInfo machineinfo.MachineInfo

	status            ServiceStatus
	statusMu          sync.Mutex
	activeLifecycles  int
	lifecyclesDrained chan struct{}
	statusEpoch       uint64
	drainClosed       bool
}

var ErrDrainingServiceCannotBeReenabled = errors.New("draining service cannot be re-enabled")
var ErrStandbyServiceRequiresFencedPromotion = errors.New("standby service requires fenced promotion")
var ErrServicePromotionStatusMismatch = errors.New("service promotion status does not match")
var ErrServicePromotionEpochMismatch = errors.New("service promotion epoch does not match")

var serviceRolesMapper = map[cfg.ServiceType]orchestratorinfo.ServiceInfoRole{
	cfg.Orchestrator:    orchestratorinfo.ServiceInfoRole_Orchestrator,
	cfg.TemplateManager: orchestratorinfo.ServiceInfoRole_TemplateBuilder,
}

func (s *ServiceInfo) GetStatus() ServiceStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	return s.status
}

func (s *ServiceInfo) GetDrainState() (orchestratorinfo.ServiceInfoStatus, uint64, bool) {
	status, epoch, drainClosed := s.GetStatusState()
	return status.Status, epoch, drainClosed
}

func (s *ServiceInfo) GetStatusState() (ServiceStatus, uint64, bool) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	return s.status, s.statusEpoch, s.drainClosed
}

func (s *ServiceInfo) SetStatus(ctx context.Context, status orchestratorinfo.ServiceInfoStatus) error {
	s.statusMu.Lock()
	if s.status.Status == orchestratorinfo.ServiceInfoStatus_Standby && status == orchestratorinfo.ServiceInfoStatus_Healthy {
		s.statusMu.Unlock()
		return ErrStandbyServiceRequiresFencedPromotion
	}
	if s.drainClosed && status == orchestratorinfo.ServiceInfoStatus_Healthy {
		s.statusMu.Unlock()
		return ErrDrainingServiceCannotBeReenabled
	}
	if s.status.Status != status {
		logger.L().Info(ctx, "Service status changed", zap.String("status", status.String()))
		s.status = ServiceStatus{Status: status, ChangedAt: time.Now()}
		s.statusEpoch++
	}
	if status == orchestratorinfo.ServiceInfoStatus_Draining {
		s.drainClosed = true
	}
	s.statusMu.Unlock()

	return nil
}

// PromoteStandby atomically promotes an observed Standby generation. It is
// intentionally narrower than SetStatus so a rollout cannot re-enable an
// unhealthy or draining process, or promote a stale process observation.
func (s *ServiceInfo) PromoteStandby(ctx context.Context, expectedEpoch uint64) error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status.Status != orchestratorinfo.ServiceInfoStatus_Standby {
		return ErrServicePromotionStatusMismatch
	}
	if s.statusEpoch != expectedEpoch {
		return ErrServicePromotionEpochMismatch
	}
	if s.drainClosed {
		return ErrDrainingServiceCannotBeReenabled
	}

	logger.L().Info(ctx, "Service status changed", zap.String("status", orchestratorinfo.ServiceInfoStatus_Healthy.String()))
	s.status = ServiceStatus{Status: orchestratorinfo.ServiceInfoStatus_Healthy, ChangedAt: time.Now()}
	s.statusEpoch++

	return nil
}

// BeginSandboxLifecycle admits lifecycle-producing work only while the service
// is healthy. The release function keeps a concurrent drain from completing
// until the admitted work can no longer create another sandbox lifecycle.
func (s *ServiceInfo) BeginSandboxLifecycle() (release func(), admitted bool) {
	s.statusMu.Lock()
	if s.status.Status != orchestratorinfo.ServiceInfoStatus_Healthy {
		s.statusMu.Unlock()
		return nil, false
	}
	if s.activeLifecycles == 0 {
		s.lifecyclesDrained = make(chan struct{})
	}
	s.activeLifecycles++
	s.statusMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.statusMu.Lock()
			defer s.statusMu.Unlock()
			s.activeLifecycles--
			if s.activeLifecycles == 0 {
				close(s.lifecyclesDrained)
				s.lifecyclesDrained = nil
			}
		})
	}, true
}

// WaitForSandboxLifecycles waits for all work admitted before a status change.
func (s *ServiceInfo) WaitForSandboxLifecycles(ctx context.Context) error {
	s.statusMu.Lock()
	drained := s.lifecyclesDrained
	s.statusMu.Unlock()
	if drained == nil {
		return nil
	}

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewInfoContainer(clientId string, version string, commit string, instanceID string, machineInfo machineinfo.MachineInfo, config cfg.Config) *ServiceInfo {
	services := cfg.GetServices(config)
	serviceRoles := make([]orchestratorinfo.ServiceInfoRole, 0)

	for _, service := range services {
		if role, ok := serviceRolesMapper[service]; ok {
			serviceRoles = append(serviceRoles, role)
		}
	}

	startup := time.Now()
	initialStatus := orchestratorinfo.ServiceInfoStatus_Healthy
	if config.StartStandby {
		initialStatus = orchestratorinfo.ServiceInfoStatus_Standby
	}
	serviceInfo := &ServiceInfo{
		ClientId:  clientId,
		ServiceId: instanceID,

		status: ServiceStatus{Status: initialStatus, ChangedAt: startup},

		Startup:     startup,
		Roles:       serviceRoles,
		Labels:      config.NodeLabels,
		MachineInfo: machineInfo,

		SourceVersion: version,
		SourceCommit:  commit,
	}

	return serviceInfo
}
