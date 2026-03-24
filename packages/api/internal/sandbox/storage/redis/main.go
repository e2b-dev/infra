package redis

import (
	"context"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

const (
	lockTimeout            = time.Minute
	transitionKeyTTL       = 70 * time.Second // Should be longer than the longest expected state transition time
	transitionResultKeyTTL = 30 * time.Second
	lockRetryInterval      = 20 * time.Millisecond
	pollInterval           = 1 * time.Second // fallback polling interval; PubSub is the primary notification mechanism
)

var _ sandbox.Storage = (*Storage)(nil)

type Storage struct {
	redisClient redis.UniversalClient
	lockService *redislock.Client
	lockOption  *redislock.Options
	subManager  *subscriptionManager
}

func (s *Storage) Name() string { return sandbox.StorageNameRedis }

func NewStorage(
	redisClient redis.UniversalClient,
) *Storage {
	return &Storage{
		redisClient: redisClient,
		lockService: redislock.New(redisClient),
		lockOption: &redislock.Options{
			RetryStrategy: newConstantBackoff(lockRetryInterval),
		},
		subManager: newSubscriptionManager(redisClient),
	}
}

// Start subscribes to the global PubSub channel and blocks until the context
// is cancelled or Close is called. It is intended to be called in a goroutine.
func (s *Storage) Start(ctx context.Context) {
	s.subManager.start(ctx)
}

// Close shuts down the subscription manager and its background goroutine.
func (s *Storage) Close() {
	s.subManager.close()
}

// Sync is here only for legacy reasons, redis backend doesn't need any sync
func (s *Storage) Sync(_ []sandbox.Sandbox, _ string) []sandbox.Sandbox {
	return nil
}
