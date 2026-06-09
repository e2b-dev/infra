// Package affinity keeps a short-lived, Redis-backed index of which nodes
// recently held which build artifacts (from SchedulingMetadata returned on
// create/resume/pause) and turns it into a bounded placement score bonus.
package affinity

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
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

// Tiers: exact current build hit > base build hit > any lineage hit.
const (
	tierExact   = "exact"
	tierBase    = "base"
	tierLineage = "lineage"
)

// Config holds the flag-driven affinity parameters. Weights are fractions of
// the Best-of-K placement score (parsed from PPM).
type Config struct {
	Enabled            bool
	TTL                time.Duration
	TopNodes           int64
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	MaxHits            float64
	ExactWeight        float64
	BaseWeight         float64
	LineageWeight      float64
	MaxBonus           float64
	MaxLineageRecorded int
}

func ConfigFromFlags(ctx context.Context, ff *featureflags.Client, contexts ...ldcontext.Context) Config {
	v := ff.JSONFlag(ctx, featureflags.SandboxPlacementCacheAffinityFlag, contexts...)

	return Config{
		Enabled:            v.GetByKey("enabled").BoolValue(),
		TTL:                time.Duration(jsonInt(v, "ttlSeconds", 28800, 60, 90000)) * time.Second,
		TopNodes:           int64(jsonInt(v, "topNodes", 20, 1, 100)),
		ReadTimeout:        time.Duration(jsonInt(v, "readTimeoutMs", 100, 10, 2000)) * time.Millisecond,
		WriteTimeout:       time.Duration(jsonInt(v, "writeTimeoutMs", 1000, 10, 5000)) * time.Millisecond,
		MaxHits:            float64(jsonInt(v, "maxHits", 10, 1, 1000)),
		ExactWeight:        jsonPPM(v, "exactWeightPpm", 2000),
		BaseWeight:         jsonPPM(v, "baseWeightPpm", 1000),
		LineageWeight:      jsonPPM(v, "lineageWeightPpm", 500),
		MaxBonus:           jsonPPM(v, "maxBonusPpm", 20000),
		MaxLineageRecorded: jsonInt(v, "maxLineageRecorded", 16, 0, 128),
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

func key(clusterID uuid.UUID, tier, buildID string) string {
	return fmt.Sprintf("placement-affinity:%s:%s:%s", clusterID, tier, buildID)
}

// Record registers that nodeID holds the artifacts described by meta.
// Best-effort and bounded: one pipeline, capped sets, short TTLs.
func (i *Index) Record(ctx context.Context, cfg Config, clusterID uuid.UUID, nodeID string, meta *orchestrator.SchedulingMetadata) {
	if i == nil || nodeID == "" || meta.GetBuildId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.WriteTimeout)
	defer cancel()

	buildID := meta.GetBuildId()
	bases := dedupe([]string{meta.GetMemfileBaseBuildId(), meta.GetRootfsBaseBuildId()}, buildID)
	lineage := lineageBuilds(meta, cfg.MaxLineageRecorded, append(bases, buildID))

	pipe := i.redis.Pipeline()
	record := func(tier string, ids ...string) {
		for _, id := range ids {
			k := key(clusterID, tier, id)
			pipe.ZIncrBy(ctx, k, 1, nodeID)
			pipe.ZRemRangeByRank(ctx, k, 0, -cfg.TopNodes-1)
			pipe.Expire(ctx, k, cfg.TTL)
		}
	}
	record(tierExact, buildID)
	record(tierBase, bases...)
	record(tierLineage, lineage...)

	if _, err := pipe.Exec(ctx); err != nil {
		logger.L().Debug(ctx, "failed to record placement affinity", zap.Error(err))
	}
}

// Scores returns a bounded per-node score bonus for the requested build ID in
// a single pipelined round trip. Returns nil on any failure so placement
// degrades to plain Best-of-K.
func (i *Index) Scores(ctx context.Context, cfg Config, clusterID uuid.UUID, buildID string) map[string]float64 {
	if i == nil || buildID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ReadTimeout)
	defer cancel()

	pipe := i.redis.Pipeline()
	tiers := []struct {
		weight float64
		cmd    *redis.ZSliceCmd
	}{
		{cfg.ExactWeight, pipe.ZRevRangeWithScores(ctx, key(clusterID, tierExact, buildID), 0, cfg.TopNodes-1)},
		{cfg.BaseWeight, pipe.ZRevRangeWithScores(ctx, key(clusterID, tierBase, buildID), 0, cfg.TopNodes-1)},
		{cfg.LineageWeight, pipe.ZRevRangeWithScores(ctx, key(clusterID, tierLineage, buildID), 0, cfg.TopNodes-1)},
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		logger.L().Debug(ctx, "failed to read placement affinity", zap.Error(err))

		return nil
	}

	scores := make(map[string]float64)
	for _, tier := range tiers {
		rows, err := tier.cmd.Result()
		if err != nil {
			continue
		}
		for _, row := range rows {
			nodeID, ok := row.Member.(string)
			if !ok || nodeID == "" {
				continue
			}
			scores[nodeID] = math.Min(scores[nodeID]+math.Min(row.Score, cfg.MaxHits)*tier.weight, cfg.MaxBonus)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	return scores
}

// lineageBuilds returns up to limit lineage build IDs, heaviest by referenced
// bytes first, excluding base/current builds (they have their own tiers).
func lineageBuilds(meta *orchestrator.SchedulingMetadata, limit int, exclude []string) []string {
	if limit <= 0 {
		return nil
	}

	bytesByID := make(map[string]uint64)
	collect := func(ids []string, sizes []uint64) {
		for n, id := range ids {
			if id == "" || slices.Contains(exclude, id) {
				continue
			}
			size := uint64(0)
			if n < len(sizes) {
				size = sizes[n]
			}
			bytesByID[id] += size
		}
	}
	collect(meta.GetMemfileBuildIds(), meta.GetMemfileBuildBytes())
	collect(meta.GetRootfsBuildIds(), meta.GetRootfsBuildBytes())

	ids := make([]string, 0, len(bytesByID))
	for id := range bytesByID {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if c := cmp.Compare(bytesByID[b], bytesByID[a]); c != 0 {
			return c
		}

		return cmp.Compare(a, b)
	})

	return ids[:min(limit, len(ids))]
}

func dedupe(ids []string, exclude string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && id != exclude && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}

	return out
}
