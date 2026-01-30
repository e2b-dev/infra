package redis

import (
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

const (
	lockTimeout      = time.Minute
	transitionKeyTTL = 70 * time.Second // Should be longer than the longest expected state transition time
	errorKeyTTL      = 5 * time.Second
	retryInterval    = 20 * time.Millisecond
)

// transitionValue represents the JSON structure for transition key values.
type transitionValue struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

var _ sandbox.Storage = (*Storage)(nil)

type Storage struct {
	redisClient redis.UniversalClient
	lockService *redislock.Client
	lockOption  *redislock.Options
}

func NewStorage(
	redisClient redis.UniversalClient,
) *Storage {
	return &Storage{
		redisClient: redisClient,
		lockService: redislock.New(redisClient),
		lockOption: &redislock.Options{
			RetryStrategy: newConstantBackoff(retryInterval),
		},
	}
}

// Sync is here only for legacy reasons, redis backend doesn't need any sync
func (s *Storage) Sync(_ []sandbox.Sandbox, _ string) []sandbox.Sandbox {
	return nil
}
