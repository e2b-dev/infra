package populate_redis

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
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
		zap.L().Error("failed to add sandbox to redis", zap.Error(err))
	}

	return nil
}

func (m *PopulateRedisStorage) Get(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	return m.memoryBackend.Get(ctx, sandboxID)
}

func (m *PopulateRedisStorage) Remove(ctx context.Context, sandboxID string) error {
	err := m.memoryBackend.Remove(ctx, sandboxID)
	if err != nil {
		return err
	}

	err = m.redisBackend.Remove(ctx, sandboxID)
	if err != nil {
		zap.L().Error("failed to remove sandbox from redis", zap.Error(err))
	}

	return nil
}

func (m *PopulateRedisStorage) Items(teamID *uuid.UUID, states []sandbox.State, options ...sandbox.ItemsOption) []sandbox.Sandbox {
	return m.memoryBackend.Items(teamID, states, options...)
}

func (m *PopulateRedisStorage) Update(ctx context.Context, sandboxID string, updateFunc func(sandbox sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	sbx, err := m.memoryBackend.Update(ctx, sandboxID, updateFunc)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	_, err = m.redisBackend.Update(ctx, sandboxID, updateFunc)
	if err != nil {
		zap.L().Error("failed to update sandbox in redis", zap.Error(err))
	}

	return sbx, nil
}

func (m *PopulateRedisStorage) StartRemoving(ctx context.Context, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(error), err error) {
	return m.memoryBackend.StartRemoving(ctx, sandboxID, stateAction)
}

func (m *PopulateRedisStorage) WaitForStateChange(ctx context.Context, sandboxID string) error {
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
