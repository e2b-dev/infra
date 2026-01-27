package redis_primary

import (
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
)

var _ sandbox.Storage = (*Storage)(nil)

type Storage struct {
	redisBackend  *redis.Storage
	memoryBackend *memory.Storage
}

func NewStorage(
	redisStorage *redis.Storage,
	memoryStorage *memory.Storage,
) *Storage {
	return &Storage{
		redisBackend:  redisStorage,
		memoryBackend: memoryStorage,
	}
}
