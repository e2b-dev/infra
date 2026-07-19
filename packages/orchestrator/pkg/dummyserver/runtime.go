package dummyserver

import (
	"errors"
	"sync"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type RuntimeState struct {
	mu      sync.Mutex
	status  orchestratorinfo.ServiceInfoStatus
	epoch   uint64
	drained bool
}

var errDummyDrainClosed = errors.New("draining service cannot be re-enabled")

func NewRuntimeState() *RuntimeState {
	return &RuntimeState{status: orchestratorinfo.ServiceInfoStatus_Healthy, epoch: 1}
}

func (s *RuntimeState) Get() (orchestratorinfo.ServiceInfoStatus, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.status, s.epoch, s.drained
}

func (s *RuntimeState) BeginLifecycle() (release func(), admitted bool) {
	s.mu.Lock()
	if s.status != orchestratorinfo.ServiceInfoStatus_Healthy {
		s.mu.Unlock()
		return nil, false
	}

	return s.mu.Unlock, true
}

func (s *RuntimeState) Snapshot() (orchestratorinfo.ServiceInfoStatus, uint64, bool, func()) {
	s.mu.Lock()

	return s.status, s.epoch, s.drained, s.mu.Unlock
}

func (s *RuntimeState) Set(status orchestratorinfo.ServiceInfoStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drained && status == orchestratorinfo.ServiceInfoStatus_Healthy {
		return errDummyDrainClosed
	}
	if s.status != status {
		s.status = status
		s.epoch++
	}
	if status == orchestratorinfo.ServiceInfoStatus_Draining {
		s.drained = true
	}

	return nil
}
