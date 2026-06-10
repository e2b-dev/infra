// Package affinity keeps a short-lived, Redis-backed index of which nodes
// recently ran which build (from SchedulingMetadata returned on
// create/resume/pause) and turns it into a bounded placement score bonus.
package affinity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Config holds the flag-driven affinity parameters. Weight is a fraction of
// the Best-of-K placement score (parsed from PPM).
type Config struct {
	Enabled      bool
	TTL          time.Duration
	TopNodes     int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxHits      float64
	Weight       float64
	MaxBonus     float64
}

func ConfigFromFlags(ctx context.Context, ff *featureflags.Client, contexts ...ldcontext.Context) Config {
	v := ff.JSONFlag(ctx, featureflags.SandboxPlacementCacheAffinityFlag, contexts...)

	return Config{
		Enabled:      v.GetByKey("enabled").BoolValue(),
		TTL:          time.Duration(jsonInt(v, "ttlSeconds", 28800, 60, 90000)) * time.Second,
		TopNodes:     int64(jsonInt(v, "topNodes", 20, 1, 100)),
		ReadTimeout:  time.Duration(jsonInt(v, "readTimeoutMs", 100, 10, 2000)) * time.Millisecond,
		WriteTimeout: time.Duration(jsonInt(v, "writeTimeoutMs", 1000, 10, 5000)) * time.Millisecond,
		MaxHits:      float64(jsonInt(v, "maxHits", 10, 1, 1000)),
		Weight:       jsonPPM(v, "weightPpm", 2000),
		MaxBonus:     jsonPPM(v, "maxBonusPpm", 20000),
	}
}

func jsonInt(v ldvalue.Value, key string, fallback, minValue, maxValue int) int {
	value := v.GetByKey(key)
	if value.IsNull() {
		return fallback
	}

	return max(minValue, min(value.IntValue(), maxValue))
}

func jsonPPM(v ldvalue.Value, key string, fallback int) float64 {
	return float64(jsonInt(v, key, fallback, 0, 100000)) / 1_000_000
}

// Index is a Redis-backed build-ID -> recent nodes index. Methods are
// nil-receiver safe so a missing Redis just disables the feature.
type Index struct {
	redis redis.UniversalClient
}

func NewIndex(redisClient redis.UniversalClient) *Index {
	if redisClient == nil {
		return nil
	}

	return &Index{redis: redisClient}
}

func key(clusterID uuid.UUID, buildID string) string {
	return fmt.Sprintf("placement-affinity:%s:%s", clusterID, buildID)
}

// Record registers that nodeID recently ran the build described by meta.
// Best-effort and bounded: one pipeline, capped set, short TTL.
func (i *Index) Record(ctx context.Context, cfg Config, clusterID uuid.UUID, nodeID string, meta *orchestrator.SchedulingMetadata) {
	if i == nil || nodeID == "" || meta.GetBuildId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.WriteTimeout)
	defer cancel()

	k := key(clusterID, meta.GetBuildId())
	pipe := i.redis.Pipeline()
	pipe.ZIncrBy(ctx, k, 1, nodeID)
	pipe.ZRemRangeByRank(ctx, k, 0, -cfg.TopNodes-1)
	pipe.Expire(ctx, k, cfg.TTL)

	if _, err := pipe.Exec(ctx); err != nil {
		logger.L().Debug(ctx, "failed to record placement affinity", zap.Error(err))
	}
}

// Scores returns a bounded per-node score bonus for the requested build ID in
// a single round trip. Returns nil on any failure so placement degrades to
// plain Best-of-K.
func (i *Index) Scores(ctx context.Context, cfg Config, clusterID uuid.UUID, buildID string) map[string]float64 {
	if i == nil || buildID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ReadTimeout)
	defer cancel()

	rows, err := i.redis.ZRevRangeWithScores(ctx, key(clusterID, buildID), 0, cfg.TopNodes-1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		logger.L().Debug(ctx, "failed to read placement affinity", zap.Error(err))

		return nil
	}

	scores := make(map[string]float64, len(rows))
	for _, row := range rows {
		nodeID, ok := row.Member.(string)
		if !ok || nodeID == "" {
			continue
		}
		scores[nodeID] = math.Min(math.Min(row.Score, cfg.MaxHits)*cfg.Weight, cfg.MaxBonus)
	}

	if len(scores) == 0 {
		return nil
	}

	return scores
}
