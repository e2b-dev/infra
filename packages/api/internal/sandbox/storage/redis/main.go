package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
	redisClient  redis.UniversalClient
	locker       *storageLocker
	subManager   *subscriptionManager
	publisher    *publisher
	featureFlags *featureflags.Client

	metrics       expirationIndexMetrics
	lockMet       lockMetrics
	transitionMet transitionMetrics
	scanMet       scanMetrics
}

// expirationIndexMetrics observes global expiration index consistency.
// indexHealed is the primary alert signal: healthy steady state is zero.
type expirationIndexMetrics struct {
	indexHealed   metric.Int64Counter
	indexRescored metric.Int64Counter

	indexSwept         metric.Int64Counter
	sweptOrphan        metric.MeasurementOption
	sweptDeadExecution metric.MeasurementOption
	sweptInvalid       metric.MeasurementOption

	// teamIndexSize records each team's sandbox index SET cardinality once per heal pass.
	teamIndexSize metric.Int64Histogram
	// indexSize records the global expiration ZSET cardinality once per heal pass.
	indexSize metric.Int64Histogram
	// sweepDuration records wall-clock time of each ExpiredItems call.
	sweepDuration metric.Int64Histogram
}

type lockMetrics struct {
	waitDuration metric.Int64Histogram
	timeouts     metric.Int64Counter
}

type transitionMetrics struct {
	callbackFailures metric.Int64Counter
	duration         metric.Int64Histogram
	joined           metric.Int64Counter
	fallbackPolls    metric.Int64Counter
	mismatches       metric.Int64Counter
}

type scanMetrics struct {
	teamsSkipped   metric.Int64Counter
	corruptRecords metric.Int64Counter
}

const sweptReasonAttr = "reason"

func newExpirationIndexMetrics(meter metric.Meter) (expirationIndexMetrics, error) {
	healed, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageExpirationIndexHealed)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("expiration index healed counter: %w", err)
	}

	rescored, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageExpirationIndexRescored)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("expiration index rescored counter: %w", err)
	}

	swept, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageExpirationIndexSwept)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("expiration index swept counter: %w", err)
	}

	teamIndexSize, err := telemetry.GetHistogram(meter, telemetry.ApiRedisStorageTeamIndexSize)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("team index size histogram: %w", err)
	}

	indexSize, err := telemetry.GetHistogram(meter, telemetry.ApiRedisStorageExpirationIndexSize)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("expiration index size histogram: %w", err)
	}

	sweepDuration, err := telemetry.GetHistogram(meter, telemetry.ApiRedisStorageExpirationIndexSweepDuration)
	if err != nil {
		return expirationIndexMetrics{}, fmt.Errorf("expiration index sweep duration histogram: %w", err)
	}

	return expirationIndexMetrics{
		indexHealed:        healed,
		indexRescored:      rescored,
		indexSwept:         swept,
		sweptOrphan:        metric.WithAttributeSet(attribute.NewSet(attribute.String(sweptReasonAttr, "orphan"))),
		sweptDeadExecution: metric.WithAttributeSet(attribute.NewSet(attribute.String(sweptReasonAttr, "dead_execution"))),
		sweptInvalid:       metric.WithAttributeSet(attribute.NewSet(attribute.String(sweptReasonAttr, "invalid"))),
		teamIndexSize:      teamIndexSize,
		indexSize:          indexSize,
		sweepDuration:      sweepDuration,
	}, nil
}

const meterScope = "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"

func newLockMetrics(meter metric.Meter) (lockMetrics, error) {
	waitDur, err := telemetry.GetHistogram(meter, telemetry.ApiRedisStorageLockWaitDuration)
	if err != nil {
		return lockMetrics{}, fmt.Errorf("lock wait duration histogram: %w", err)
	}

	timeouts, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageLockTimeouts)
	if err != nil {
		return lockMetrics{}, fmt.Errorf("lock timeouts counter: %w", err)
	}

	return lockMetrics{waitDuration: waitDur, timeouts: timeouts}, nil
}

func newTransitionMetrics(meter metric.Meter) (transitionMetrics, error) {
	cbFails, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageTransitionCallbackFailures)
	if err != nil {
		return transitionMetrics{}, fmt.Errorf("transition callback failures counter: %w", err)
	}

	dur, err := telemetry.GetHistogram(meter, telemetry.ApiRedisStorageTransitionDuration)
	if err != nil {
		return transitionMetrics{}, fmt.Errorf("transition duration histogram: %w", err)
	}

	joined, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageTransitionJoined)
	if err != nil {
		return transitionMetrics{}, fmt.Errorf("transition joined counter: %w", err)
	}

	fallbacks, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageTransitionFallbackPolls)
	if err != nil {
		return transitionMetrics{}, fmt.Errorf("transition fallback polls counter: %w", err)
	}

	mismatches, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageTransitionExecutionMismatches)
	if err != nil {
		return transitionMetrics{}, fmt.Errorf("transition execution mismatches counter: %w", err)
	}

	return transitionMetrics{
		callbackFailures: cbFails,
		duration:         dur,
		joined:           joined,
		fallbackPolls:    fallbacks,
		mismatches:       mismatches,
	}, nil
}

func newScanMetrics(meter metric.Meter) (scanMetrics, error) {
	skipped, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageScanTeamsSkipped)
	if err != nil {
		return scanMetrics{}, fmt.Errorf("scan teams skipped counter: %w", err)
	}

	corrupt, err := telemetry.GetCounter(meter, telemetry.ApiRedisStorageScanCorruptRecords)
	if err != nil {
		return scanMetrics{}, fmt.Errorf("scan corrupt records counter: %w", err)
	}

	return scanMetrics{teamsSkipped: skipped, corruptRecords: corrupt}, nil
}

func NewStorage(
	redisClient redis.UniversalClient,
	meterProvider metric.MeterProvider,
	featureFlags *featureflags.Client,
) (*Storage, error) {
	meter := meterProvider.Meter(meterScope)

	subManager := newSubscriptionManager(redisClient, globalStorageNotifyChannel)
	pub, err := newPublisher(redisClient, globalStorageNotifyChannel, meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}

	metrics, err := newExpirationIndexMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create expiration index metrics: %w", err)
	}

	lockMet, err := newLockMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock metrics: %w", err)
	}

	transitionMet, err := newTransitionMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create transition metrics: %w", err)
	}

	scanMet, err := newScanMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to create scan metrics: %w", err)
	}

	return &Storage{
		redisClient:   redisClient,
		locker:        newStorageLocker(redisClient, subManager, pub),
		subManager:    subManager,
		publisher:     pub,
		featureFlags:  featureFlags,
		metrics:       metrics,
		lockMet:       lockMet,
		transitionMet: transitionMet,
		scanMet:       scanMet,
	}, nil
}

// Start subscribes to the global PubSub channel and launches the publish
// worker and the expiration index healer. Blocks until the context is
// cancelled or Close is called.
func (s *Storage) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
func (s *Storage) Reconcile(ctx context.Context, sbxs []sandboxtypes.NodeSandbox, nodeID string) []sandboxtypes.NodeSandbox {
	if len(sbxs) == 0 {
		return nil
	}

	now := time.Now()

	// Filter out sandboxes that are too young to be considered orphans.
	type candidate struct {
		sbx sandboxtypes.NodeSandbox
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

	var orphans []sandboxtypes.NodeSandbox
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
