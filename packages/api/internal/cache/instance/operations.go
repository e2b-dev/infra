package instance

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type StateAction string

const (
	StateActionPause StateAction = "pause"
	StateActionKill  StateAction = "kill"
)

// Add the sandbox to the cache
func (ms *MemoryStore) Add(ctx context.Context, sandbox Sandbox, newlyCreated bool) {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
	)

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	added := ms.items.SetIfAbsent(sandbox.SandboxID, newMemorySandbox(sandbox))
	if !added {
		zap.L().Warn("Sandbox already exists in cache", logger.WithSandboxID(sandbox.SandboxID))
		return
	}

	for _, callback := range ms.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range ms.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}
	// Release the reservation if it exists
	ms.reservations.release(sandbox.SandboxID)
}

// Exists Check if the sandbox exists in the cache or is being evicted.
func (ms *MemoryStore) Exists(sandboxID string) bool {
	return ms.items.Has(sandboxID)
}

// Get the item from the cache.
func (ms *MemoryStore) get(sandboxID string) (*memorySandbox, error) {
	item, ok := ms.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item, nil
}

// Get the item from the cache.
func (ms *MemoryStore) Get(sandboxID string, includeEvicting bool) (Sandbox, error) {
	item, ok := ms.items.Get(sandboxID)
	if !ok {
		return Sandbox{}, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	data := item.Data()

	if data.IsExpired() && !includeEvicting {
		return Sandbox{}, fmt.Errorf("sandbox \"%s\" is being evicted", sandboxID)
	}

	return data, nil
}

func (ms *MemoryStore) Remove(sandboxID string) {
	ms.items.Remove(sandboxID)
}

func (ms *MemoryStore) Items(teamID *uuid.UUID) []Sandbox {
	items := make([]Sandbox, 0)
	for _, item := range ms.items.Items() {
		data := item.Data()
		if data.IsExpired() {
			continue
		}

		if teamID != nil && data.TeamID != *teamID {
			continue
		}

		items = append(items, data)
	}

	return items
}

func (ms *MemoryStore) ItemsToEvict() []Sandbox {
	items := make([]Sandbox, 0)
	for _, item := range ms.items.Items() {
		data := item.Data()
		if !data.IsExpired() {
			continue
		}

		if data.State != StateRunning {
			continue
		}

		items = append(items, data)
	}

	return items
}

func (ms *MemoryStore) ItemsByState(teamID *uuid.UUID, states []State) map[State][]Sandbox {
	items := make(map[State][]Sandbox)
	for _, item := range ms.items.Items() {
		data := item.Data()
		if teamID != nil && data.TeamID != *teamID {
			continue
		}

		if slices.Contains(states, data.State) {
			if _, ok := items[data.State]; !ok {
				items[data.State] = []Sandbox{}
			}

			items[data.State] = append(items[data.State], data)
		}
	}

	return items
}

func (ms *MemoryStore) Len(teamID *uuid.UUID) int {
	return len(ms.Items(teamID))
}

func (ms *MemoryStore) ExtendEndTime(sandboxID string, newEndTime time.Time, allowShorter bool) (bool, error) {
	item, ok := ms.items.Get(sandboxID)
	if !ok {
		return false, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item.extendEndTime(newEndTime, allowShorter), nil
}

func (ms *MemoryStore) StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error) {
	sbx, err := ms.get(sandboxID)
	if err != nil {
		return false, nil, err
	}

	return startRemoving(ctx, sbx, stateAction)
}

func startRemoving(ctx context.Context, sbx *memorySandbox, stateAction StateAction) (alreadyDone bool, callback func(error), err error) {
	newState := StateKilling
	if stateAction == StateActionPause {
		newState = StatePausing
	}

	sbx.mu.Lock()
	transition := sbx.transition
	if transition != nil {
		currentState := sbx._data.State
		sbx.mu.Unlock()

		if currentState != newState && !allowed[currentState][newState] {
			return false, nil, fmt.Errorf("invalid state transition, already in transition from %s", currentState)
		}

		zap.L().Debug("State transition already in progress to the same state, waiting", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)))
		err = transition.WaitWithContext(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("sandbox is in failed state: %w", err)
		}

		// If the transition is to the same state just wait
		switch {
		case currentState == newState:
			return true, func(err error) {}, nil
		case allowed[currentState][newState]:
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

	if _, ok := allowed[sbx._data.State][newState]; !ok {
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

func (ms *MemoryStore) WaitForStateChange(ctx context.Context, sandboxID string) error {
	sbx, err := ms.get(sandboxID)
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
