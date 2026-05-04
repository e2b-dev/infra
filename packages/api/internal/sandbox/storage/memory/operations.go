package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Add the sandbox to the cache
func (s *Storage) Add(_ context.Context, sbx sandbox.Sandbox) error {
	added := s.items.SetIfAbsent(sbx.SandboxID, newMemorySandbox(sbx))
	if !added {
		return sandbox.ErrAlreadyExists
	}

	return nil
}

// exists check if the sandbox exists in the cache or is being evicted.
func (s *Storage) exists(sandboxID string) bool {
	return s.items.Has(sandboxID)
}

// Get the item from the cache.
func (s *Storage) get(sandboxID string) (*memorySandbox, error) {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item, nil
}

// Get the item from the cache.
func (s *Storage) Get(_ context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	data := item.Data()
	if data.TeamID != teamID {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	return data, nil
}

func (s *Storage) Remove(_ context.Context, _ uuid.UUID, sandboxID string) error {
	s.items.Remove(sandboxID)

	return nil
}

func (s *Storage) getItems(teamID *uuid.UUID, states []sandbox.State) []sandbox.Sandbox {
	items := make([]sandbox.Sandbox, 0)

	s.items.IterCb(func(_ string, item *memorySandbox) {
		data := item.Data()

		if teamID != nil && *teamID != data.TeamID {
			return
		}

		if len(states) > 0 && !slices.Contains(states, data.State) {
			return
		}

		items = append(items, data)
	})

	return items
}

func (s *Storage) TeamItems(_ context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	return s.getItems(&teamID, states), nil
}

func (s *Storage) TeamsWithSandboxCount(_ context.Context) (map[uuid.UUID]int64, error) {
	teams := make(map[uuid.UUID]int64)

	s.items.IterCb(func(_ string, item *memorySandbox) {
		teams[item._data.TeamID]++
	})

	return teams, nil
}

func (s *Storage) ExpiredItems(_ context.Context) ([]sandbox.Sandbox, error) {
	now := time.Now()
	expired := make([]sandbox.Sandbox, 0)

	s.items.IterCb(func(_ string, item *memorySandbox) {
		sbx := item.Data()
		if sbx.State != sandbox.StateRunning {
			return
		}

		if sbx.IsExpired(now) {
			expired = append(expired, sbx)
		}
	})

	return expired, nil
}

func (s *Storage) Update(_ context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	item, ok := s.items.Get(sandboxID)
	if !ok {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	item.mu.Lock()
	defer item.mu.Unlock()

	if item._data.TeamID != teamID {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	sbx, err := updateFunc(item._data)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	item._data = sbx

	return sbx, nil
}

func (s *Storage) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) (sandbox.Sandbox, bool, func(context.Context, error), error) {
	sbx, err := s.get(sandboxID)
	if err != nil {
		return sandbox.Sandbox{}, false, nil, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	data := sbx.Data()
	if data.TeamID != teamID {
		return sandbox.Sandbox{}, false, nil, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
	}

	alreadyDone, callback, err := startRemoving(ctx, sbx, opts)

	return sbx.Data(), alreadyDone, callback, err
}

func startRemoving(ctx context.Context, sbx *memorySandbox, opts sandbox.RemoveOpts) (alreadyDone bool, callback func(ctx context.Context, err error), err error) {
	sbx.mu.Lock()
	transition := sbx.transition

	// Resolve eviction under the lock + re-check expiry
	if opts.Eviction {
		// If there's a transition already in place, don't evict.
		if transition != nil {
			sbx.mu.Unlock()

			return false, nil, sandbox.ErrEvictionInProgress
		}

		// If sandbox isn't expired (e.g. race condition with KeepAliveFor), skip.
		if !sbx._data.IsExpired(time.Now()) {
			sbx.mu.Unlock()

			return false, nil, sandbox.ErrEvictionNotNeeded
		}
	}

	newState := opts.Action.TargetState

	if transition != nil {
		currentState := sbx._data.State
		sbx.mu.Unlock()

		if currentState != newState && !sandbox.AllowedTransitions[currentState][newState] {
			return false, nil, &sandbox.InvalidStateTransitionError{CurrentState: currentState, TargetState: newState}
		}

		logger.L().Debug(ctx, "State transition already in progress to the same state, waiting", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)))
		err = transition.WaitWithContext(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("sandbox is in failed state: %w", err)
		}

		// If the transition is to the same state just wait
		switch {
		case currentState == newState:
			return true, func(context.Context, error) {}, nil
		case sandbox.AllowedTransitions[currentState][newState]:
			return startRemoving(ctx, sbx, sandbox.RemoveOpts{Action: opts.Action})
		default:
			return false, nil, errors.New("unexpected state transition")
		}
	}

	defer sbx.mu.Unlock()
	if sbx._data.State == newState {
		logger.L().Debug(ctx, "Already in the same state", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)))

		return true, func(context.Context, error) {}, nil
	}

	if _, ok := sandbox.AllowedTransitions[sbx._data.State][newState]; !ok {
		return false, nil, &sandbox.InvalidStateTransitionError{CurrentState: sbx._data.State, TargetState: newState}
	}

	if opts.Action.Effect == sandbox.TransitionExpires {
		sbx.setExpired()
	}

	sbx._data.State = newState
	sbx.transition = utils.NewErrorOnce()

	callback = func(ctx context.Context, err error) {
		logger.L().Debug(ctx, "Transition complete", logger.WithSandboxID(sbx.SandboxID()), zap.String("state", string(newState)), zap.Error(err))
		sbx.mu.Lock()
		defer sbx.mu.Unlock()

		if opts.Action.Effect == sandbox.TransitionTransient {
			if err == nil && sbx._data.State == newState {
				sbx._data.State = sandbox.StateRunning
			}

			// Signal nil to waiters so concurrent callers (e.g. kill)
			// are unblocked and can proceed with their own transition.
			err = nil
		}

		setErr := sbx.transition.SetError(err)
		if setErr != nil {
			logger.L().Warn(ctx, "Failed to set transition result", logger.WithSandboxID(sbx.SandboxID()), zap.Error(setErr))
		}

		if err != nil {
			// Keep the transition in place so the error stays
			return
		}

		// The transition is completed and the next transition can be started
		sbx.transition = nil
	}

	return false, callback, nil
}

func (s *Storage) WaitForStateChange(ctx context.Context, _ uuid.UUID, sandboxID string) error {
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

// MarkKilled records that a sandbox was killed with the given reason.
func (s *Storage) MarkKilled(_ context.Context, teamID uuid.UUID, sandboxID string, reason sandbox.KillReason) error {
	info := sandbox.KillInfo{
		Reason:   reason,
		KilledAt: time.Now().UnixMilli(),
	}
	s.killed.Set(killedKey(teamID.String(), sandboxID), info)
	return nil
}

// WasKilled checks if a sandbox was killed and returns the kill info.
func (s *Storage) WasKilled(_ context.Context, teamID uuid.UUID, sandboxID string) (*sandbox.KillInfo, error) {
	info, ok := s.killed.Get(killedKey(teamID.String(), sandboxID))
	if !ok {
		return nil, nil
	}
	return &info, nil
}
