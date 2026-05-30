package orchestrator

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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	placementAffinityMinTimeoutMs                 = 10
	placementAffinityMaxTimeoutMs                 = 2000
	defaultPlacementAffinityTTLSeconds            = 90000
	defaultPlacementAffinityTopNodes              = 20
	defaultPlacementAffinityReadTimeoutMs         = 100
	defaultPlacementAffinityWriteTimeoutMs        = 1000
	defaultPlacementAffinityMaxScore              = 10
	defaultPlacementAffinityMaxScoreBonusPpm      = 20000
	defaultPlacementAffinityBuildWeightPpm        = 1000
	defaultPlacementAffinityTemplateWeightPpm     = 500
	defaultPlacementAffinityBaseTemplateWeightPpm = 250
)

type placementAffinity struct {
	redis redis.UniversalClient
}

type placementAffinityConfig struct {
	enabled            bool
	ttl                time.Duration
	topNodes           int64
	readTimeout        time.Duration
	writeTimeout       time.Duration
	maxAffinityScore   float64
	maxScoreBonus      float64
	buildWeight        float64
	templateWeight     float64
	baseTemplateWeight float64
}

func newPlacementAffinity(redisClient redis.UniversalClient) *placementAffinity {
	if redisClient == nil {
		return nil
	}

	return &placementAffinity{redis: redisClient}
}

func placementAffinityConfigFromFlags(ctx context.Context, ff *featureflags.Client, contexts ...ldcontext.Context) placementAffinityConfig {
	v := ff.JSONFlag(ctx, featureflags.SandboxPlacementCacheAffinityFlag, contexts...)

	return placementAffinityConfig{
		enabled:            v.GetByKey("enabled").BoolValue(),
		ttl:                time.Duration(jsonInt(v, "ttlSeconds", defaultPlacementAffinityTTLSeconds, 60, 90000)) * time.Second,
		topNodes:           int64(jsonInt(v, "topNodes", defaultPlacementAffinityTopNodes, 1, 100)),
		readTimeout:        time.Duration(jsonInt(v, "readTimeoutMs", defaultPlacementAffinityReadTimeoutMs, placementAffinityMinTimeoutMs, placementAffinityMaxTimeoutMs)) * time.Millisecond,
		writeTimeout:       time.Duration(jsonInt(v, "writeTimeoutMs", defaultPlacementAffinityWriteTimeoutMs, placementAffinityMinTimeoutMs, placementAffinityMaxTimeoutMs)) * time.Millisecond,
		maxAffinityScore:   float64(jsonInt(v, "maxAffinityScore", defaultPlacementAffinityMaxScore, 1, 1000)),
		maxScoreBonus:      jsonPPM(v, "maxScoreBonusPpm", defaultPlacementAffinityMaxScoreBonusPpm),
		buildWeight:        jsonPPM(v, "buildWeightPpm", defaultPlacementAffinityBuildWeightPpm),
		templateWeight:     jsonPPM(v, "templateWeightPpm", defaultPlacementAffinityTemplateWeightPpm),
		baseTemplateWeight: jsonPPM(v, "baseTemplateWeightPpm", defaultPlacementAffinityBaseTemplateWeightPpm),
	}
}

func jsonInt(v ldvalue.Value, key string, fallback, minValue, maxValue int) int {
	value := v.GetByKey(key)
	if value.IsNull() {
		return fallback
	}

	return clampInt(value.IntValue(), minValue, maxValue)
}

func jsonPPM(v ldvalue.Value, key string, fallback int) float64 {
	return float64(jsonInt(v, key, fallback, 0, 100000)) / 1000000
}

func clampInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}

	return v
}

func placementAffinityKey(clusterID uuid.UUID, kind, id string) string {
	return fmt.Sprintf("placement-affinity:%s:%s:%s", clusterID, kind, id)
}

func placementAffinityID(teamID, id string) string {
	if id == "" || teamID == "" {
		return id
	}

	return teamID + ":" + id
}

func (a *placementAffinity) record(ctx context.Context, cfg placementAffinityConfig, clusterID uuid.UUID, teamID, nodeID, buildID, templateID, baseTemplateID string) {
	if a == nil || nodeID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.writeTimeout)
	defer cancel()

	pipe := a.redis.Pipeline()
	hasCommands := false
	for _, item := range []struct {
		kind   string
		id     string
		weight float64
	}{
		{kind: "build", id: buildID, weight: cfg.buildWeight},
		{kind: "template", id: placementAffinityID(teamID, templateID), weight: cfg.templateWeight},
		{kind: "base-template", id: placementAffinityID(teamID, baseTemplateID), weight: cfg.baseTemplateWeight},
	} {
		if item.id == "" || item.weight == 0 {
			continue
		}

		key := placementAffinityKey(clusterID, item.kind, item.id)
		pipe.ZIncrBy(ctx, key, 1, nodeID)
		pipe.ZRemRangeByRank(ctx, key, 0, -cfg.topNodes-1)
		pipe.Expire(ctx, key, cfg.ttl)
		hasCommands = true
	}
	if !hasCommands {
		return
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		logger.L().Debug(ctx, "failed to record placement affinity", zap.Error(err))
	}
}

func (a *placementAffinity) scores(ctx context.Context, cfg placementAffinityConfig, clusterID uuid.UUID, teamID, buildID, templateID, baseTemplateID string) map[string]float64 {
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.readTimeout)
	defer cancel()

	pipe := a.redis.Pipeline()
	type affinityCmd struct {
		cmd    *redis.ZSliceCmd
		weight float64
	}
	cmds := make([]affinityCmd, 0, 3)
	for _, item := range []struct {
		kind   string
		id     string
		weight float64
	}{
		{kind: "build", id: buildID, weight: cfg.buildWeight},
		{kind: "template", id: placementAffinityID(teamID, templateID), weight: cfg.templateWeight},
		{kind: "base-template", id: placementAffinityID(teamID, baseTemplateID), weight: cfg.baseTemplateWeight},
	} {
		if item.id == "" || item.weight == 0 {
			continue
		}

		key := placementAffinityKey(clusterID, item.kind, item.id)
		cmds = append(cmds, affinityCmd{
			cmd:    pipe.ZRevRangeWithScores(ctx, key, 0, cfg.topNodes-1),
			weight: item.weight,
		})
	}
	if len(cmds) == 0 {
		return nil
	}

	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		logger.L().Debug(ctx, "failed to read placement affinity", zap.Error(err))

		return nil
	}

	scores := make(map[string]float64)
	for _, cmd := range cmds {
		rows, err := cmd.cmd.Result()
		if err != nil {
			continue
		}

		for _, row := range rows {
			nodeID, ok := row.Member.(string)
			if !ok || nodeID == "" {
				continue
			}
			scores[nodeID] += math.Min(row.Score, cfg.maxAffinityScore) * cmd.weight
			scores[nodeID] = math.Min(scores[nodeID], cfg.maxScoreBonus)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	return scores
}
