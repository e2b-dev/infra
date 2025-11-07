package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Add stores a sandbox in Redis
func (s *Storage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	redisCtx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	// Adjust end time if needed
	if sbx.EndTime.Sub(sbx.StartTime) > sbx.MaxInstanceLength {
		sbx.EndTime = sbx.StartTime.Add(sbx.MaxInstanceLength)
	}

	// Serialize sandbox
	data, err := json.Marshal(sbx)
	if err != nil {
		zap.L().Error("Failed to marshal sandbox", logger.WithSandboxID(sbx.SandboxID), zap.Error(err))

		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	key := getSandboxKey(sbx.SandboxID)

	// Storage in Redis with expiration
	err = s.redisClient.Set(redisCtx, key, data, sbx.MaxInstanceLength+time.Minute).Err()
	if err != nil {
		zap.L().Error("Failed to store sandbox in Redis", logger.WithSandboxID(sbx.SandboxID), zap.Error(err))

		return fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	return nil
}

// Get retrieves a sandbox from Redis
func (s *Storage) Get(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

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
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	// Remove from Redis
	key := getSandboxKey(sandboxID)
	s.redisClient.Del(ctx, key)

	return nil
}

// Items returns sandboxes matching the given filters
func (s *Storage) Items(_ *uuid.UUID, _ []sandbox.State, _ ...sandbox.ItemsOption) []sandbox.Sandbox {
	// TODO: Implement later
	return nil
}

// Update modifies a sandbox atomically using a Lua script
func (s *Storage) Update(sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()

	key := getSandboxKey(sandboxID)
	var updatedSbx sandbox.Sandbox

	// Use WATCH for optimistic locking
	err := s.redisClient.Watch(ctx, func(tx *redis.Tx) error {
		// Get current value
		data, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return &sandbox.NotFoundError{SandboxID: sandboxID}
		}
		if err != nil {
			return err
		}

		var sbx sandbox.Sandbox
		err = json.Unmarshal(data, &sbx)
		if err != nil {
			return err
		}

		// Apply update
		newSbx, err := updateFunc(sbx)
		if err != nil {
			return err
		}

		updatedSbx = newSbx

		// Serialize updated sandbox
		newData, err := json.Marshal(newSbx)
		if err != nil {
			return err
		}

		// Execute transaction
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, newData, redis.KeepTTL)

			return nil
		})

		return err
	}, key)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	return updatedSbx, nil
}

// StartRemoving initiates the removal process for a sandbox
func (s *Storage) StartRemoving(_ context.Context, _ string, _ sandbox.StateAction) (alreadyDone bool, callback func(error), err error) {
	// TODO: Implement later
	return false, nil, nil
}

// WaitForStateChange waits for a sandbox state to change
func (s *Storage) WaitForStateChange(_ context.Context, _ string) error {
	// TODO: Implement later
	return nil
}
