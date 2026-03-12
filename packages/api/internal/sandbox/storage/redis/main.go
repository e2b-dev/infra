package redis

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	lockTimeout            = time.Minute
	transitionKeyTTL       = 70 * time.Second // Should be longer than the longest expected state transition time
	transitionResultKeyTTL = 30 * time.Second
	lockRetryInterval      = 20 * time.Millisecond
	pollInterval           = 1 * time.Second // fallback polling interval; PubSub is the primary notification mechanism

	// orphanGracePeriod is the minimum age a sandbox must have before it can be
	// considered an orphan and killed. This prevents killing sandboxes that are
	// still in the process of being created (store write happens before the VM starts).
	orphanGracePeriod = time.Minute
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
func (s *Storage) Sync(ctx context.Context, sbxs []sandbox.Sandbox, nodeID string) []sandbox.Sandbox {
	now := time.Now()
	var orphans []sandbox.Sandbox

	for _, sbx := range sbxs {
		// Skip sandboxes that were started recently — they may still be in the
		// process of being fully registered in the store.
		if now.Sub(sbx.StartTime) < orphanGracePeriod {
			continue
		}

		_, err := s.Get(ctx, sbx.TeamID, sbx.SandboxID)
		if err == nil {
			// Sandbox exists in store, not an orphan.
			continue
		}

		if !errors.Is(err, sandbox.ErrNotFound) {
			// Store error (e.g. Redis connection issue) — skip to avoid mass kills.
			logger.L().Warn(ctx, "Error checking sandbox in store during orphan cleanup, skipping",
				zap.Error(err),
				logger.WithSandboxID(sbx.SandboxID),
				logger.WithNodeID(nodeID),
			)

			continue
		}

		// Sandbox is an orphan — kill it on the node.
		logger.L().Error(ctx, "Killing orphaned sandbox not found in store",
			logger.WithSandboxID(sbx.SandboxID),
			logger.WithNodeID(nodeID),
		)

		orphans = append(orphans, sbx)
	}

	return orphans
}
