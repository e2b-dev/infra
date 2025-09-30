package memory

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Add the sandbox to the cache
func (s *Store) Add(ctx context.Context, sandbox sandbox.Sandbox, newlyCreated bool) {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
	)

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	added := s.items.SetIfAbsent(sandbox.SandboxID, newMemorySandbox(sandbox))
	if !added {
		zap.L().Warn("Sandbox already exists in cache", logger.WithSandboxID(sandbox.SandboxID))
		return
	}

	for _, callback := range s.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range s.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}
	// Release the reservation if it exists
	s.reservations.release(sandbox.SandboxID)
}

// exists check if the sandbox exists in the cache or is being evicted.
func (s *Store) exists(sandboxID string) bool {
	return s.items.Has(sandboxID)
}

// Get the item from the cache.
func (s *Store) get(sandboxID string) (*memorySandbox, error) {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item, nil
}

// Get the item from the cache.
func (s *Store) Get(sandboxID string, includeEvicting bool) (sandbox.Sandbox, error) {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	data := item.Data()

	if data.IsExpired() && !includeEvicting {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox \"%s\" is being evicted", sandboxID)
	}

	return data, nil
}

func (s *Store) Remove(sandboxID string) {
	s.items.Remove(sandboxID)
}

func (s *Store) Items(options ...sandbox.ItemsOption) []sandbox.Sandbox {
	filter := &sandbox.ItemsFilter{}
	for _, opt := range options {
		opt(filter)
	}

	items := make([]sandbox.Sandbox, 0)
	for _, item := range s.items.Items() {
		data := item.Data()

		if !applyFilter(data, filter) {
			continue
		}

		items = append(items, data)
	}

	return items
}

func (s *Store) Update(sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, bool)) bool {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return false
	}

	item.mu.Lock()
	defer item.mu.Unlock()
	newData, ok := updateFunc(item._data)
	if ok {
		item._data = newData
	}

	return ok
}

func (s *Store) StartRemoving(ctx context.Context, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(error), err error) {
	sbx, err := s.get(sandboxID)
	if err != nil {
		return false, nil, err
	}

	return startRemoving(ctx, sbx, stateAction)
}

func startRemoving(ctx context.Context, sbx *memorySandbox, stateAction sandbox.StateAction) (alreadyDone bool, callback func(error), err error) {
	newState := sandbox.StateKilling
	if stateAction == sandbox.StateActionPause {
		newState = sandbox.StatePausing
	}

	sbx.mu.Lock()
	transition := sbx.transition
	if transition != nil {
		currentState := sbx._data.State
		sbx.mu.Unlock()

		if currentState != newState && !sandbox.AllowedTransitions[currentState][newState] {
			return false, nil, fmt.Errorf("invalid state transition, already in transition from %s", currentState)
		}

		zap.L().Debug("States transition already in progress to the same state, waiting", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)))
		err = transition.WaitWithContext(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("sandbox is in failed state: %w", err)
		}

		// If the transition is to the same state just wait
		switch {
		case currentState == newState:
			return true, func(err error) {}, nil
		case sandbox.AllowedTransitions[currentState][newState]:
			return startRemoving(ctx, sbx, stateAction)
		default:
			return false, nil, fmt.Errorf("unexpected state transition")
		}
	}

	defer sbx.mu.Unlock()
	if sbx._data.State == newState {
		zap.L().Debug("Already in the same state", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)))
		return true, func(error) {}, nil
	}

	if _, ok := sandbox.AllowedTransitions[sbx._data.State][newState]; !ok {
		return false, nil, fmt.Errorf("invalid state transition from %s to %s", sbx._data.State, newState)
	}

	sbx.setExpired()
	sbx._data.State = newState
	sbx.transition = utils.NewErrorOnce()

	callback = func(err error) {
		zap.L().Debug("Transition complete", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)), zap.Error(err))
		sbx.mu.Lock()
		defer sbx.mu.Unlock()

		setErr := sbx.transition.SetError(err)
		if err != nil {
			// Keep the transition in place so the error stays
			zap.L().Error("Failed to set transition result", logger.WithSandboxID(sbx.SandboxID()), zap.Error(setErr))
			return
		}

		// The transition is completed and the next transition can be started
		sbx.transition = nil
	}

	return false, callback, nil
}

func (s *Store) WaitForStateChange(ctx context.Context, sandboxID string) error {
	sbx, err := s.get(sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox: %w", err)
	}
	return waitForStateChange(ctx, sbx)
}

func waitForStateChange(ctx context.Context, sbx *memorySandbox) error {
	sbx.mu.RLock()
	transition := sbx.transition
	sbx.mu.RUnlock()
	if transition == nil {
		return nil
	}

	return transition.WaitWithContext(ctx)
}
