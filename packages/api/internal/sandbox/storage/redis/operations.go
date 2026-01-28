package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// Add stores a sandbox in Redis atomically with its team index entry
func (s *Storage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	// Serialize sandbox
	data, err := json.Marshal(sbx)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	key := getSandboxKey(sbx.TeamID.String(), sbx.SandboxID)
	teamKey := getTeamIndexKey(sbx.TeamID.String())

	// Store sandbox and add to team index atomically using a transaction
	_, err = s.redisClient.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, key, data, 0)
		pipe.SAdd(ctx, teamKey, sbx.SandboxID)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	return nil
}

// Get retrieves a sandbox from Redis
func (s *Storage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	key := getSandboxKey(teamID.String(), sandboxID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{SandboxID: sandboxID}
	}
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to get sandbox from Redis: %w", err)
	}

	var sbx sandbox.Sandbox
	err = json.Unmarshal(data, &sbx)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to unmarshal sandbox: %w", err)
	}

	return sbx, nil
}

// Remove deletes a sandbox from Redis atomically with its team index entry
func (s *Storage) Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	key := getSandboxKey(teamID.String(), sandboxID)
	teamKey := getTeamIndexKey(teamID.String())

	lock, err := s.lockService.Obtain(ctx, redis_utils.GetLockKey(key), lockTimeout, s.lockOption)
	if err != nil {
		return fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "Failed to release lock", zap.Error(err))
		}
	}()

	// Remove sandbox and its team index entry atomically using a transaction
	_, err = s.redisClient.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		pipe.SRem(ctx, teamKey, sandboxID)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from Redis: %w", err)
	}

	return nil
}

// TeamItems retrieves sandboxes for a specific team, filtered by states and options
func (s *Storage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	// Get sandbox IDs from team index
	teamKey := getTeamIndexKey(teamID.String())
	sandboxIDs, err := s.redisClient.SMembers(ctx, teamKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox IDs from team index: %w", err)
	}

	if len(sandboxIDs) == 0 {
		return []sandbox.Sandbox{}, nil
	}

	// Build keys and batch fetch with MGET
	keys := make([]string, len(sandboxIDs))
	for i, id := range sandboxIDs {
		keys[i] = getSandboxKey(teamID.String(), id)
	}

	results, err := s.redisClient.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get sandboxes from Redis: %w", err)
	}

	// Build state set for efficient lookup
	stateSet := make(map[sandbox.State]bool)
	for _, state := range states {
		stateSet[state] = true
	}

	// Deserialize and filter
	var sandboxes []sandbox.Sandbox
	for _, rawResult := range results {
		if rawResult == nil {
			continue // Stale index entry - sandbox was deleted
		}

		var sbx sandbox.Sandbox
		result, ok := rawResult.(string)
		if !ok {
			logger.L().Error(ctx, "Invalid sandbox data type in Redis")

			continue
		}

		if err := json.Unmarshal([]byte(result), &sbx); err != nil {
			logger.L().Error(ctx, "Failed to unmarshal sandbox", zap.Error(err))

			continue
		}

		// Filter by state if states are specified
		if len(states) > 0 && !stateSet[sbx.State] {
			continue
		}

		sandboxes = append(sandboxes, sbx)
	}

	return sandboxes, nil
}

// Update modifies a sandbox atomically
func (s *Storage) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	key := getSandboxKey(teamID.String(), sandboxID)
	var updatedSbx sandbox.Sandbox

	lock, err := s.lockService.Obtain(ctx, redis_utils.GetLockKey(key), lockTimeout, s.lockOption)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "Failed to release lock", zap.Error(err))
		}
	}()

	// Get current value
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{SandboxID: sandboxID}
	}
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	var sbx sandbox.Sandbox
	err = json.Unmarshal(data, &sbx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	// Apply update
	newSbx, err := updateFunc(sbx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	updatedSbx = newSbx

	// Serialize updated sandbox
	newData, err := json.Marshal(newSbx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	// Execute transaction
	err = s.redisClient.Set(ctx, key, newData, redis.KeepTTL).Err()
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	return updatedSbx, nil
}

func (s *Storage) AllItems(_ context.Context, _ []sandbox.State, _ ...sandbox.ItemsOption) ([]sandbox.Sandbox, error) {
	return nil, nil
}

// StartRemoving initiates the removal process for a sandbox
func (s *Storage) StartRemoving(_ context.Context, _ uuid.UUID, _ string, _ sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	// TODO: Implement later (ENG-3285)
	return false, nil, nil
}

// WaitForStateChange waits for a sandbox state to change
func (s *Storage) WaitForStateChange(_ context.Context, _ uuid.UUID, _ string) error {
	// TODO: Implement later (ENG-3285)
	return nil
}
