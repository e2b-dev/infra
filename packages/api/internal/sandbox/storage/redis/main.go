package redis

import (
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

const (
	sandboxKeyPrefix = "sandbox:"
	redisTimeout     = time.Second * 5
)

var _ sandbox.Storage = (*Storage)(nil)

type Storage struct {
	redisClient redis.UniversalClient
}

func NewStorage(
	redisClient redis.UniversalClient,
) *Storage {
	return &Storage{
		redisClient: redisClient,
	}
}

// Sync is here only for legacy reasons, redis backend doesn't need any sync
func (s *Storage) Sync(_ []sandbox.Sandbox, _ string) []sandbox.Sandbox {
	return nil
}
