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

	lock, err := s.lockService.Obtain(ctx, key, lockTimeout, nil)
	if err != nil {
		return fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(ctx)
		if err != nil {
			zap.L().Error("Failed to release lock", zap.Error(err))
		}
	}()

	err = s.redisClient.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from Redis: %w", err)
	}

	return nil
}

// Items returns sandboxes matching the given filters
func (s *Storage) Items(_ *uuid.UUID, _ []sandbox.State, _ ...sandbox.ItemsOption) []sandbox.Sandbox {
	// TODO: Implement later (ENG-3312)
	return nil
}

// Update modifies a sandbox atomically using a Lua script
func (s *Storage) Update(ctx context.Context, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	key := getSandboxKey(sandboxID)
	var updatedSbx sandbox.Sandbox

	lock, err := s.lockService.Obtain(ctx, key, lockTimeout, nil)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(ctx)
		if err != nil {
			zap.L().Error("Failed to release lock", zap.Error(err))
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
func (s *Storage) StartRemoving(_ context.Context, _ string, _ sandbox.StateAction) (alreadyDone bool, callback func(error), err error) {
	// TODO: Implement later (ENG-3285)
	return false, nil, nil
}

// WaitForStateChange waits for a sandbox state to change
func (s *Storage) WaitForStateChange(_ context.Context, _ string) error {
	// TODO: Implement later (ENG-3285)
	return nil
}
