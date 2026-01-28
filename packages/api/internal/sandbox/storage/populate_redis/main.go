package populate_redis

import (
	"context"
	"errors"

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

func (m *PopulateRedisStorage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	return m.memoryBackend.Get(ctx, teamID, sandboxID)
}

func (m *PopulateRedisStorage) Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	err := m.memoryBackend.Remove(ctx, teamID, sandboxID)
	if err != nil {
		return err
	}

	err = m.redisBackend.Remove(ctx, teamID, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "failed to remove sandbox from redis", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	return nil
}

func (m *PopulateRedisStorage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	return m.memoryBackend.TeamItems(ctx, teamID, states)
}

func (m *PopulateRedisStorage) AllItems(ctx context.Context, states []sandbox.State, options ...sandbox.ItemsOption) ([]sandbox.Sandbox, error) {
	return m.memoryBackend.AllItems(ctx, states, options...)
}

func (m *PopulateRedisStorage) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	sbx, err := m.memoryBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	_, err = m.redisBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		if !errors.Is(err, sandbox.ErrCannotShortenTTL) {
			logger.L().Error(ctx, "failed to update sandbox in redis", zap.Error(err), logger.WithSandboxID(sandboxID))
		}
	}

	return sbx, nil
}

func (m *PopulateRedisStorage) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	return m.memoryBackend.StartRemoving(ctx, teamID, sandboxID, stateAction)
}

func (m *PopulateRedisStorage) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return m.memoryBackend.WaitForStateChange(ctx, teamID, sandboxID)
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
