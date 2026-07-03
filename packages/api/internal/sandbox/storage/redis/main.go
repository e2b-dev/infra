package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	lockTimeout            = time.Minute
	transitionKeyTTL       = 70 * time.Second // Should be longer than the longest expected state transition time
	transitionResultKeyTTL = 30 * time.Second
	lockRetryMinInterval   = 200 * time.Millisecond
	lockRetryMaxInterval   = time.Second
	lockRetryJitter        = 0.25
	pollInterval           = 1 * time.Second // fallback polling interval; PubSub is the primary notification mechanism

	// orphanGracePeriod is the minimum age a sandbox must have before it can be
	// considered an orphan and killed. This prevents killing sandboxes that are
	// still in the process of being created (store write happens before the VM starts).
	orphanGracePeriod = time.Minute
)

var _ sandboxtypes.Storage = (*Storage)(nil)

type Storage struct {
	redisClient redis.UniversalClient
	locker      *storageLocker
	subManager  *subscriptionManager
	publisher   *publisher
}

const meterScope = "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"

func NewStorage(
	redisClient redis.UniversalClient,
	meterProvider metric.MeterProvider,
) (*Storage, error) {
	meter := meterProvider.Meter(meterScope)

	subManager := newSubscriptionManager(redisClient, globalStorageNotifyChannel)
	pub, err := newPublisher(redisClient, globalStorageNotifyChannel, meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}

	return &Storage{
		redisClient: redisClient,
		locker:      newStorageLocker(redisClient, subManager, pub),
		subManager:  subManager,
		publisher:   pub,
	}, nil
}

// Start subscribes to the global PubSub channel and launches the publish
// worker and the expiration index healer. Blocks until the context is
// cancelled or Close is called.
func (s *Storage) Start(ctx context.Context) {
	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		s.publisher.run(ctx)
	}()

	go s.startHealer(ctx)

	s.subManager.start(ctx)
	<-pubDone
}

// Close shuts down the subscription manager and the publish worker.
func (s *Storage) Close(ctx context.Context) {
	s.subManager.close()
	s.publisher.close(ctx)
}

// Reconcile returns a list of sandboxes that are considered orphans on the current node.
func (s *Storage) Reconcile(ctx context.Context, sbxs []sandboxtypes.Sandbox, nodeID string) []sandboxtypes.Sandbox {
	if len(sbxs) == 0 {
		return nil
	}

	now := time.Now()

	// Filter out sandboxes that are too young to be considered orphans.
	type candidate struct {
		sbx sandboxtypes.Sandbox
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

	var orphans []sandboxtypes.Sandbox
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
