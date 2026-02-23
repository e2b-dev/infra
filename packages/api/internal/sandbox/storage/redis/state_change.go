package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// StartRemoving initiates a state transition for a sandbox using atomic Lua scripts.
//
// The function handles concurrent requests safely:
//  1. Acquires a distributed lock on the sandbox
//  2. Checks if there's an ongoing transition via the transition key:
//     - If the same target state is in progress, waits for completion and returns the result
//     - If a different state is in progress, waits for it to complete, then retries
//     - If no transition is in progress, start the transition
//  3. Validates the state transition is allowed
//  4. Atomically updates the sandbox state and sets the transition key with a unique ID
//  5. Returns a callback that the caller must invoke to signal completion
//
// The callback is critical: it deletes the transition key
// and sets the result value with short TTL to notify waiters of the outcome.
func (s *Storage) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	newState := stateAction.TargetState

	key := getSandboxKey(teamID.String(), sandboxID)
	transitionKey := getTransitionKey(teamID.String(), sandboxID)

	// Acquire distributed lock
	lock, err := s.lockService.Obtain(ctx, redis_utils.GetLockKey(key), lockTimeout, s.lockOption)
	if err != nil {
		return false, nil, fmt.Errorf("failed to obtain lock: %w", err)
	}

	// Ensure lock is released once
	releaseFunc := sync.OnceValue(func() error {
		return lock.Release(context.WithoutCancel(ctx))
	})

	defer func() {
		releaseErr := releaseFunc()
		if releaseErr != nil {
			logger.L().Error(ctx, "Failed to release lock", zap.Error(releaseErr))
		}
	}()

	// Get current sandbox state first
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil, &sandbox.NotFoundError{SandboxID: sandboxID}
	}
	if err != nil {
		return false, nil, fmt.Errorf("failed to get sandbox from Redis: %w", err)
	}

	var sbx sandbox.Sandbox
	if err = json.Unmarshal(data, &sbx); err != nil {
		return false, nil, fmt.Errorf("failed to unmarshal sandbox: %w", err)
	}

	// Check if there's an existing transition
	transactionID, err := s.redisClient.Get(ctx, transitionKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, nil, fmt.Errorf("failed to check transition key: %w", err)
	}

	if transactionID != "" {
		releaseErr := releaseFunc()
		if releaseErr != nil {
			logger.L().Warn(ctx, "Failed to release lock before waiting", zap.Error(releaseErr))
		}

		return s.handleExistingTransition(ctx, teamID, sbx, stateAction, newState, transactionID)
	}

	// Check if already in target state
	if sbx.State == newState {
		logger.L().Debug(ctx, "Already in the same state", logger.WithSandboxID(sandboxID), zap.String("state", string(newState)))

		return true, func(context.Context, error) {}, nil
	}

	// Validate state transition is allowed
	if !sandbox.AllowedTransitions[sbx.State][newState] {
		return false, nil, &sandbox.InvalidStateTransitionError{CurrentState: sbx.State, TargetState: newState}
	}

	// Update sandbox state
	sbx.State = newState
	if stateAction.Effect == sandbox.TransitionExpires {
		if !sbx.IsExpired() {
			sbx.EndTime = time.Now()
		}
	}

	newData, err := json.Marshal(sbx)
	if err != nil {
		return false, nil, fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	// Generate transition ID
	transitionID := uuid.New().String()
	resultKey := getTransitionResultKey(teamID.String(), sandboxID, transitionID)

	// Use atomic Lua script to update sandbox and set transition key with UUID
	ttlSeconds := int(transitionKeyTTL.Seconds())
	resultTtlSeconds := int(transitionResultKeyTTL.Seconds())

	err = startTransitionScript.Run(ctx, s.redisClient, []string{key, transitionKey, resultKey}, newData, transitionID, ttlSeconds, resultTtlSeconds).Err()
	if err != nil {
		return false, nil, fmt.Errorf("failed to update sandbox state: %w", err)
	}

	logger.L().Debug(ctx, "Started state transition", logger.WithSandboxID(sandboxID), zap.String("state", string(newState)), zap.String("transitionID", transitionID))

	return false, s.createCallback(teamID, sandboxID, transitionKey, resultKey, transitionID, stateAction), nil
}

// createCallback returns a callback function for completing a transition.
// For transient actions, it first restores the sandbox state to Running.
// On success, the callback deletes the transition key and sets empty result.
// On error, the callback deletes the transition key and sets error message in result.
func (s *Storage) createCallback(teamID uuid.UUID, sandboxID, transitionKey, resultKey, transitionID string, stateAction sandbox.StateAction) func(context.Context, error) {
	return func(cbCtx context.Context, cbErr error) {
		logger.L().Debug(cbCtx, "Transition complete", logger.WithSandboxID(sandboxID), zap.String("state", string(stateAction.TargetState)), zap.String("transitionID", transitionID), zap.Error(cbErr))

		var restoreErr error
		if stateAction.Effect == sandbox.TransitionTransient && cbErr == nil {
			restoreErr = s.restoreToRunning(cbCtx, teamID, sandboxID, stateAction.TargetState)
		}

		lock, err := s.lockService.Obtain(cbCtx, redis_utils.GetLockKey(transitionKey), lockTimeout, s.lockOption)
		if err != nil {
			logger.L().Warn(cbCtx, "Failed to obtain lock in callback", logger.WithSandboxID(sandboxID), zap.String("transitionID", transitionID), zap.Error(err))

			return
		}
		defer func() {
			err = lock.Release(context.WithoutCancel(cbCtx))
			if err != nil {
				logger.L().Error(cbCtx, "Failed to release lock in callback", logger.WithSandboxID(sandboxID), zap.String("transitionID", transitionID), zap.Error(err))
			}
		}()

		// Determine result value for waiters:
		// - Restore failure: propagate so callers know state is inconsistent
		// - Transient original failure: signal success so concurrent ops
		//   (e.g. kill) can proceed — matching the memory implementation
		// - Non-transient failure: propagate the error
		resultValue := ""
		if restoreErr != nil {
			resultValue = fmt.Errorf("failed to restore sandbox to running: %w", restoreErr).Error()
		} else if cbErr != nil && stateAction.Effect != sandbox.TransitionTransient {
			resultValue = cbErr.Error()
		}

		// Set result key with short TTL
		setErr := s.redisClient.Set(cbCtx, resultKey, resultValue, transitionResultKeyTTL).Err()
		if setErr != nil {
			logger.L().Warn(cbCtx, "Failed to set transition result", logger.WithSandboxID(sandboxID), zap.String("transitionID", transitionID), zap.Error(setErr))
		}

		// Delete transition key
		delErr := s.redisClient.Del(cbCtx, transitionKey).Err()
		if delErr != nil {
			logger.L().Warn(cbCtx, "Failed to delete transition key", logger.WithSandboxID(sandboxID), zap.Error(delErr))
		}
	}
}

// restoreToRunning restores the sandbox to Running if it is still in the given transient state.
func (s *Storage) restoreToRunning(ctx context.Context, teamID uuid.UUID, sandboxID string, fromState sandbox.State) error {
	_, err := s.Update(ctx, teamID, sandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != fromState {
			return sbx, nil
		}

		sbx.State = sandbox.StateRunning

		return sbx, nil
	})

	return err
}

// WaitForStateChange waits for a sandbox state transition to complete.
func (s *Storage) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	transitionKey := getTransitionKey(teamID.String(), sandboxID)
	transactionID, err := s.redisClient.Get(ctx, transitionKey).Result()
	if errors.Is(err, redis.Nil) {
		logger.L().Debug(ctx, "No ongoing transition", logger.WithSandboxID(sandboxID))

		return nil
	} else if err != nil {
		return fmt.Errorf("failed to check transition key: %w", err)
	}

	return s.waitForTransition(ctx, teamID, sandboxID, transactionID)
}

// waitForTransition waits for a specific transition to complete.
func (s *Storage) waitForTransition(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID,
	transitionID string,
) error {
	transitionKey := getTransitionKey(teamID.String(), sandboxID)

	for {
		currentTransitionID, err := s.redisClient.Get(ctx, transitionKey).Result()
		if errors.Is(err, redis.Nil) || transitionID != currentTransitionID {
			// Transition key gone or new transition started - check the result
			return s.checkTransitionResult(ctx, getTransitionResultKey(teamID.String(), sandboxID, transitionID))
		}
		if err != nil {
			return fmt.Errorf("failed to check transition key: %w", err)
		}

		// Wait before the next poll
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
			// Continue polling
		}
	}
}

// checkTransitionResult checks the result key for the outcome of a completed transition.
func (s *Storage) checkTransitionResult(ctx context.Context, resultKey string) error {
	result, err := s.redisClient.Get(ctx, resultKey).Result()
	if errors.Is(err, redis.Nil) {
		// Result expired or never set - assume success (transition key was deleted)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check transition result: %w", err)
	}

	if result != "" {
		return fmt.Errorf("transition failed: %s", result)
	}

	return nil
}

func (s *Storage) handleExistingTransition(
	ctx context.Context,
	teamID uuid.UUID,
	sbx sandbox.Sandbox,
	stateAction sandbox.StateAction,
	newState sandbox.State,
	transactionID string,
) (bool, func(context.Context, error), error) {
	if sbx.State == newState {
		// Same target state - wait for completion and return alreadyDone=true
		logger.L().Debug(ctx, "State transition already in progress to the same state, waiting",
			logger.WithSandboxID(sbx.SandboxID),
			zap.String("state", string(newState)))

		err := s.waitForTransition(ctx, teamID, sbx.SandboxID, transactionID)
		if err != nil {
			return false, nil, fmt.Errorf("failed waiting for transition: %w", err)
		}

		return true, func(context.Context, error) {}, nil
	}

	// Different state - validate transition and wait
	if !sandbox.AllowedTransitions[sbx.State][newState] {
		return false, nil, &sandbox.InvalidStateTransitionError{CurrentState: sbx.State, TargetState: newState}
	}

	err := s.waitForTransition(ctx, teamID, sbx.SandboxID, transactionID)
	if err != nil {
		return false, nil, fmt.Errorf("failed waiting for transition: %w", err)
	}

	// Retry with new state after transition completes
	return s.StartRemoving(ctx, teamID, sbx.SandboxID, stateAction)
}
