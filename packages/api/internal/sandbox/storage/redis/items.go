package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ExpiredItems returns all sandboxes matching that have expired.
func (s *Storage) ExpiredItems(ctx context.Context) ([]sandbox.Sandbox, error) {
	nowMs := float64(time.Now().UnixMilli())

	// Fetch members whose score (EndTime in ms) is <= now.
	expiredMembers, err := s.redisClient.ZRangeByScore(ctx, globalExpirationSet, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%v", nowMs),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query global expiration index: %w", err)
	}

	if len(expiredMembers) == 0 {
		return nil, nil
	}

	teamSandboxes := make(map[string][]string) // teamID -> []sandboxID
	for _, member := range expiredMembers {
		teamID, sandboxID, ok := parseExpirationMember(member)
		if !ok {
			logger.L().Warn(ctx, "Invalid expiration index member", zap.String("member", member))

			continue
		}

		teamSandboxes[teamID] = append(teamSandboxes[teamID], sandboxID)
	}

	pipe := s.redisClient.Pipeline()
	var batches []*redis.SliceCmd
	for teamID, sandboxIDs := range teamSandboxes {
		keys := make([]string, len(sandboxIDs))
		for i, id := range sandboxIDs {
			keys[i] = getSandboxKey(teamID, id)
		}

		cmd := pipe.MGet(ctx, keys...)
		batches = append(batches, cmd)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("MGET pipeline failed: %w", err)
	}

	// Deserialize and filter by state; double-check IsExpired in case the index is slightly stale.
	var result []sandbox.Sandbox
	for _, cmd := range batches {
		for _, raw := range cmd.Val() {
			if raw == nil {
				continue
			}

			str, ok := raw.(string)
			if !ok {
				continue
			}

			var sbx sandbox.Sandbox
			if err := json.Unmarshal([]byte(str), &sbx); err != nil {
				logger.L().Error(ctx, "Failed to unmarshal sandbox", zap.Error(err))

				continue
			}

			result = append(result, sbx)
		}
	}

	return result, nil
}
