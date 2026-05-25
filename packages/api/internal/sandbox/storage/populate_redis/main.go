package populate_redis

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var _ sandboxtypes.Storage = (*PopulateRedisStorage)(nil)

type PopulateRedisStorage struct {
	memoryBackend *memory.Storage
	redisBackend  *redis.Storage
}

func (m *PopulateRedisStorage) Name() string { return sandboxtypes.StorageNamePopulateRedis }

func (m *PopulateRedisStorage) Add(ctx context.Context, sandbox sandboxtypes.Sandbox) error {
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

func (m *PopulateRedisStorage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandboxtypes.Sandbox, error) {
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

func (m *PopulateRedisStorage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandboxtypes.State) ([]sandboxtypes.Sandbox, error) {
	return m.memoryBackend.TeamItems(ctx, teamID, states)
}

func (m *PopulateRedisStorage) ExpiredItems(ctx context.Context) ([]sandboxtypes.Sandbox, error) {
	return m.memoryBackend.ExpiredItems(ctx)
}

func (m *PopulateRedisStorage) TeamsWithSandboxCount(ctx context.Context) (map[uuid.UUID]int64, error) {
	return m.memoryBackend.TeamsWithSandboxCount(ctx)
}

func (m *PopulateRedisStorage) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox sandboxtypes.Sandbox) (sandboxtypes.Sandbox, error)) (sandboxtypes.Sandbox, error) {
	sbx, err := m.memoryBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		return sandboxtypes.Sandbox{}, err
	}

	_, err = m.redisBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		logger.L().Error(ctx, "failed to update sandbox in redis", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	return sbx, nil
}

func (m *PopulateRedisStorage) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandboxtypes.RemoveOpts) (sandboxtypes.Sandbox, bool, func(context.Context, error), error) {
	return m.memoryBackend.StartRemoving(ctx, teamID, sandboxID, opts)
}

func (m *PopulateRedisStorage) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return m.memoryBackend.WaitForStateChange(ctx, teamID, sandboxID)
}

func (m *PopulateRedisStorage) Reconcile(ctx context.Context, sandboxes []sandboxtypes.Sandbox, nodeID string) []sandboxtypes.Sandbox {
	return m.memoryBackend.Reconcile(ctx, sandboxes, nodeID)
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
