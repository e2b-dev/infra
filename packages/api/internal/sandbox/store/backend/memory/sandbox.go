package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type sandbox struct {
	base *store.Sandbox

	mu sync.RWMutex

	stopping *utils.SetOnce[struct{}]
}

func newSandbox(
	sbx *store.Sandbox,
) *sandbox {
	return &sandbox{
		base:     sbx,
		stopping: utils.NewSetOnce[struct{}](),
		mu:       sync.RWMutex{},
	}
}

func (s *sandbox) Base() *store.Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.base
}

func (s *sandbox) markRemoving(removeType store.RemoveType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.base.State != store.StateRunning {
		if s.base.State == store.StatePausing || s.base.State == store.StatePaused {
			return store.ErrAlreadyBeingPaused
		} else {
			return store.ErrAlreadyBeingDeleted
		}
	}
	// Set remove type
	if removeType == store.RemoveTypePause {
		s.base.State = store.StatePausing
	} else {
		s.base.State = store.StateKilling
	}

	// Mark the stop time
	s.setExpired()

	return nil
}

func (s *sandbox) IsExpired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.isExpired()
}

func (s *sandbox) isExpired() bool {
	return s.base.IsExpired()
}

func (s *sandbox) GetEndTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.base.EndTime
}

func (s *sandbox) SetEndTime(endTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.setEndTime(endTime)
}

func (s *sandbox) setEndTime(endTime time.Time) {
	s.base.EndTime = endTime
}

func (s *sandbox) SetExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setExpired()
}

func (s *sandbox) setExpired() {
	if !s.isExpired() {
		s.setEndTime(time.Now())
	}
}

func (s *sandbox) WaitForStop(ctx context.Context) error {
	if s.base.State == store.StateRunning {
		return fmt.Errorf("sandbox isn't stopping")
	}

	_, err := s.stopping.WaitWithContext(ctx)
	return err
}

func (s *sandbox) stopDone(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.base.State == store.StatePausing {
		s.base.State = store.StatePaused
	} else {
		s.base.State = store.StateKilled
	}

	if err != nil {
		err := s.stopping.SetError(err)
		if err != nil {
			zap.L().Error("error setting stopDone value", zap.Error(err))
		}
	} else {
		err := s.stopping.SetValue(struct{}{})
		if err != nil {
			zap.L().Error("error setting stopDone value", zap.Error(err))
		}
	}
}
