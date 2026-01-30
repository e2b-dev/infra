package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// Add stores a sandbox in Redis
func (s *Storage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	// Serialize sandbox
	data, err := json.Marshal(sbx)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	key := getSandboxKey(sbx.SandboxID)

	// Storage in Redis with max expiration little bit longer than max instance length to prevent leaking
	err = s.redisClient.Set(ctx, key, data, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	return nil
}

// Get retrieves a sandbox from Redis
func (s *Storage) Get(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	key := getSandboxKey(sandboxID)
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

// Remove deletes a sandbox from Redis
func (s *Storage) Remove(ctx context.Context, sandboxID string) error {
	// Remove from Redis
	key := getSandboxKey(sandboxID)

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

	err = s.redisClient.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from Redis: %w", err)
	}

	return nil
}

// Items returns sandboxes matching the given filters
func (s *Storage) Items(teamID *uuid.UUID, states []sandbox.State, options ...sandbox.ItemsOption) []sandbox.Sandbox {
	return s.items(context.Background(), teamID, states, options...)
}

func (s *Storage) items(ctx context.Context, teamID *uuid.UUID, states []sandbox.State, options ...sandbox.ItemsOption) []sandbox.Sandbox {
	filter := sandbox.NewItemsFilter()
	for _, opt := range options {
		opt(filter)
	}

	pattern := fmt.Sprintf("%s*", sandboxKeyPrefix)
	cursor := uint64(0)
	result := make([]sandbox.Sandbox, 0)

	for {
		keys, nextCursor, err := s.redisClient.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			logger.L().Error(ctx, "failed to scan sandboxes from redis", zap.Error(err))

			break
		}

		for _, key := range keys {
			data, err := s.redisClient.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				logger.L().Error(ctx, "failed to get sandbox from redis", zap.Error(err), zap.String("sandbox_key", key))

				continue
			}

			var sbx sandbox.Sandbox
			err = json.Unmarshal(data, &sbx)
			if err != nil {
				logger.L().Error(ctx, "failed to unmarshal sandbox from redis", zap.Error(err), zap.String("sandbox_key", key))

				continue
			}

			if teamID != nil && *teamID != sbx.TeamID {
				continue
			}

			if states != nil && !slices.Contains(states, sbx.State) {
				continue
			}

			if filter.OnlyExpired && !sbx.IsExpired() {
				continue
			}

			result = append(result, sbx)
		}

		if nextCursor == 0 {
			break
		}

		cursor = nextCursor
	}

	return result
}

// Update modifies a sandbox atomically using a Lua script
func (s *Storage) Update(ctx context.Context, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	key := getSandboxKey(sandboxID)
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

// StartRemoving initiates the removal process for a sandbox
func (s *Storage) StartRemoving(_ context.Context, _ string, _ sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	// TODO: Implement later (ENG-3285)
	return false, nil, nil
}

// WaitForStateChange waits for a sandbox state to change
func (s *Storage) WaitForStateChange(_ context.Context, _ string) error {
	// TODO: Implement later (ENG-3285)
	return nil
}
