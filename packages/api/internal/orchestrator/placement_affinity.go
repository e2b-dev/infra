package orchestrator

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	placementAffinityTTL          = 4 * time.Hour
	placementAffinityTop          = 20
	placementAffinityReadTimeout  = 100 * time.Millisecond
	placementAffinityWriteTimeout = time.Second

	buildAffinityWeight        = 0.001
	templateAffinityWeight     = 0.0005
	baseTemplateAffinityWeight = 0.00025
	maxAffinityScore           = 10
	maxTotalAffinityScoreBonus = 0.02
)

type placementAffinity struct {
	redis redis.UniversalClient
}

func newPlacementAffinity(redisClient redis.UniversalClient) *placementAffinity {
	if redisClient == nil {
		return nil
	}

	return &placementAffinity{redis: redisClient}
}

func placementAffinityKey(clusterID uuid.UUID, kind, id string) string {
	return fmt.Sprintf("placement-affinity:%s:%s:%s", clusterID, kind, id)
}

func (a *placementAffinity) record(ctx context.Context, clusterID uuid.UUID, nodeID, buildID, templateID, baseTemplateID string) {
	if a == nil || nodeID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), placementAffinityWriteTimeout)
	defer cancel()

	pipe := a.redis.Pipeline()
	hasCommands := false
	for _, item := range []struct {
		kind string
		id   string
	}{
		{kind: "build", id: buildID},
		{kind: "template", id: templateID},
		{kind: "base-template", id: baseTemplateID},
	} {
		if item.id == "" {
			continue
		}

		key := placementAffinityKey(clusterID, item.kind, item.id)
		pipe.ZIncrBy(ctx, key, 1, nodeID)
		pipe.ZRemRangeByRank(ctx, key, 0, -placementAffinityTop-1)
		pipe.Expire(ctx, key, placementAffinityTTL)
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

func (a *placementAffinity) scores(ctx context.Context, clusterID uuid.UUID, buildID, templateID, baseTemplateID string) map[string]float64 {
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, placementAffinityReadTimeout)
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
		{kind: "build", id: buildID, weight: buildAffinityWeight},
		{kind: "template", id: templateID, weight: templateAffinityWeight},
		{kind: "base-template", id: baseTemplateID, weight: baseTemplateAffinityWeight},
	} {
		if item.id == "" {
			continue
		}

		key := placementAffinityKey(clusterID, item.kind, item.id)
		cmds = append(cmds, affinityCmd{
			cmd:    pipe.ZRevRangeWithScores(ctx, key, 0, placementAffinityTop-1),
			weight: item.weight,
		})
	}
	if len(cmds) == 0 {
		return nil
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
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
			scores[nodeID] += math.Min(row.Score, maxAffinityScore) * cmd.weight
			scores[nodeID] = math.Min(scores[nodeID], maxTotalAffinityScoreBonus)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	return scores
}
