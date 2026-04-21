package redis

import (
	"context"
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

// Reconcile returns a list of sandboxes that are considered orphans on the current node.
func (s *Storage) Reconcile(ctx context.Context, sbxs []sandbox.Sandbox, nodeID string) []sandbox.Sandbox {
	if len(sbxs) == 0 {
		return nil
	}

	now := time.Now()

	// Filter out sandboxes that are too young to be considered orphans.
	type candidate struct {
		sbx sandbox.Sandbox
		key string
	}

	// Group candidates by team for per-team MGET (Redis Cluster slot compatibility).
	teamCandidates := make(map[string][]candidate)
	for _, sbx := range sbxs {
		if now.Sub(sbx.StartTime) < orphanGracePeriod {
			continue
		}

		team := sbx.TeamID.String()
		teamCandidates[team] = append(teamCandidates[team], candidate{
			sbx: sbx,
			key: getSandboxKey(team, sbx.SandboxID),
		})
	}
	if len(teamCandidates) == 0 {
		return nil
	}

	// Pipeline per-team MGET calls.
	pipe := s.redisClient.Pipeline()

	type batchInfo struct {
		cmd        *redis.SliceCmd
		candidates []candidate
	}

	var batches []batchInfo
	for _, candidates := range teamCandidates {
		keys := make([]string, len(candidates))
		for i, c := range candidates {
			keys[i] = c.key
		}

		cmd := pipe.MGet(ctx, keys...)
		batches = append(batches, batchInfo{cmd: cmd, candidates: candidates})
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		// Pipeline error — skip entirely to avoid mass kills.
		logger.L().Error(ctx, "Redis pipeline error during orphan sync, skipping",
			zap.Error(err),
			logger.WithNodeID(nodeID),
		)

		return nil
	}

	var orphans []sandbox.Sandbox
	for _, batch := range batches {
		results := batch.cmd.Val()
		for i, raw := range results {
			if raw != nil {
				// Sandbox exists in store, not an orphan.
				continue
			}

			sbx := batch.candidates[i].sbx
			logger.L().Warn(ctx, "Killing orphaned sandbox not found in store",
				logger.WithSandboxID(sbx.SandboxID),
				logger.WithNodeID(nodeID),
			)

			orphans = append(orphans, sbx)
		}
	}

	return orphans
}
