package populate_redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var _ sandbox.Storage = (*PopulateRedisStorage)(nil)

type PopulateRedisStorage struct {
	memoryBackend *memory.Storage
	redisBackend  *redis.Storage
}

func (m *PopulateRedisStorage) Add(ctx context.Context, sandbox sandbox.Sandbox) error {
	err := m.memoryBackend.Add(ctx, sandbox)
	if err != nil {
		return err
	}

	err = m.redisBackend.Add(ctx, sandbox)
	if err != nil {
		logger.L().Error(ctx, "failed to add sandbox to redis", zap.Error(err))
	}

	return nil
}

func (m *PopulateRedisStorage) Get(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	sbx, err := m.memoryBackend.Get(ctx, sandboxID)
	if err == nil {
		return sbx, nil
	}

	var notFoundErr *sandbox.NotFoundError
	if errors.As(err, &notFoundErr) {
		sbx, err = m.redisBackend.Get(ctx, sandboxID)
		if err != nil {
			return sandbox.Sandbox{}, err
		}

		err = m.memoryBackend.Add(ctx, sbx)
		if err != nil {
			logger.L().Debug(ctx, "failed to add sandbox to memory", zap.Error(err))
		}

		return sbx, nil
	}

	return sandbox.Sandbox{}, err
}

func (m *PopulateRedisStorage) Remove(ctx context.Context, sandboxID string) error {
	err := m.memoryBackend.Remove(ctx, sandboxID)
	if err != nil {
		return err
	}

	err = m.redisBackend.Remove(ctx, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "failed to remove sandbox from redis", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	return nil
}

func (m *PopulateRedisStorage) Items(teamID *uuid.UUID, states []sandbox.State, options ...sandbox.ItemsOption) []sandbox.Sandbox {
	memoryItems := m.memoryBackend.Items(teamID, states, options...)
	redisItems := m.redisBackend.Items(teamID, states, options...)

	if len(redisItems) == 0 {
		return memoryItems
	}
	if len(memoryItems) == 0 {
		return redisItems
	}

	items := make(map[string]sandbox.Sandbox, len(redisItems)+len(memoryItems))
	for _, sbx := range redisItems {
		items[sbx.SandboxID] = sbx
	}
	for _, sbx := range memoryItems {
		items[sbx.SandboxID] = sbx
	}

	result := make([]sandbox.Sandbox, 0, len(items))
	for _, sbx := range items {
		result = append(result, sbx)
	}

	return result
}

func (m *PopulateRedisStorage) Update(ctx context.Context, sandboxID string, updateFunc func(sandbox sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	updatedSbx, err := m.redisBackend.Update(ctx, sandboxID, updateFunc)
	if err != nil {
		var notFoundErr *sandbox.NotFoundError
		if !errors.As(err, &notFoundErr) {
			return sandbox.Sandbox{}, err
		}

		memorySbx, memoryErr := m.memoryBackend.Update(ctx, sandboxID, updateFunc)
		if memoryErr != nil {
			return sandbox.Sandbox{}, memoryErr
		}

		addErr := m.redisBackend.Add(ctx, memorySbx)
		if addErr != nil {
			logger.L().Error(ctx, "failed to add sandbox to redis", zap.Error(addErr))
		}

		return memorySbx, nil
	}

	_, memoryErr := m.memoryBackend.Update(ctx, sandboxID, func(_ sandbox.Sandbox) (sandbox.Sandbox, error) {
		return updatedSbx, nil
	})
	if memoryErr != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(memoryErr, &notFoundErr) {
			addErr := m.memoryBackend.Add(ctx, updatedSbx)
			if addErr != nil {
				logger.L().Debug(ctx, "failed to add sandbox to memory during update", zap.Error(addErr))
			}
		} else {
			return sandbox.Sandbox{}, memoryErr
		}
	}

	return updatedSbx, nil
}

func (m *PopulateRedisStorage) StartRemoving(ctx context.Context, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	_, err = m.memoryBackend.Get(ctx, sandboxID)
	if err != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(err, &notFoundErr) {
			sbx, redisErr := m.redisBackend.Get(ctx, sandboxID)
			if redisErr != nil {
				return false, nil, redisErr
			}

			addErr := m.memoryBackend.Add(ctx, sbx)
			if addErr != nil {
				logger.L().Debug(ctx, "failed to add sandbox to memory before removing", zap.Error(addErr))
			}
		} else {
			return false, nil, err
		}
	}

	alreadyDone, callback, err = m.memoryBackend.StartRemoving(ctx, sandboxID, stateAction)
	if err != nil {
		return false, nil, err
	}

	_, redisErr := m.redisBackend.Update(ctx, sandboxID, func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		newState := sandbox.StateKilling
		if stateAction == sandbox.StateActionPause {
			newState = sandbox.StatePausing
		}

		if sbx.State == newState {
			return sbx, nil
		}

		if _, ok := sandbox.AllowedTransitions[sbx.State][newState]; !ok {
			return sandbox.Sandbox{}, fmt.Errorf("invalid state transition from %s to %s", sbx.State, newState)
		}

		if !sbx.IsExpired() {
			sbx.EndTime = time.Now()
		}

		sbx.State = newState

		return sbx, nil
	})
	if redisErr != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(redisErr, &notFoundErr) {
			sbx, memoryErr := m.memoryBackend.Get(ctx, sandboxID)
			if memoryErr != nil {
				logger.L().Error(ctx, "failed to load sandbox from memory for redis update", zap.Error(memoryErr))
			} else {
				addErr := m.redisBackend.Add(ctx, sbx)
				if addErr != nil {
					logger.L().Error(ctx, "failed to add sandbox to redis during start removing", zap.Error(addErr))
				}
			}
		} else {
			logger.L().Error(ctx, "failed to update sandbox in redis during start removing", zap.Error(redisErr))
		}
	}

	return alreadyDone, callback, nil
}

func (m *PopulateRedisStorage) WaitForStateChange(ctx context.Context, sandboxID string) error {
	_, err := m.memoryBackend.Get(ctx, sandboxID)
	if err != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(err, &notFoundErr) {
			sbx, redisErr := m.redisBackend.Get(ctx, sandboxID)
			if redisErr != nil {
				return redisErr
			}

			addErr := m.memoryBackend.Add(ctx, sbx)
			if addErr != nil {
				logger.L().Debug(ctx, "failed to add sandbox to memory before waiting", zap.Error(addErr))
			}
		} else {
			return err
		}
	}

	return m.memoryBackend.WaitForStateChange(ctx, sandboxID)
}

func (m *PopulateRedisStorage) Sync(sandboxes []sandbox.Sandbox, nodeID string) []sandbox.Sandbox {
	return m.memoryBackend.Sync(sandboxes, nodeID)
}

func NewStorage(
	memoryStorage *memory.Storage,
	redisStorage *redis.Storage,
) *PopulateRedisStorage {
	return &PopulateRedisStorage{
		memoryBackend: memoryStorage,
		redisBackend:  redisStorage,
	}
}
