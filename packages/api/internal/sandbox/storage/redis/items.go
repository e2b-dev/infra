package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const expiredItemsBatchSize = 256

// ExpiredItems returns running sandboxes whose EndTime has passed.
// It bounds per-cycle work via LIMIT and cleans up orphaned ZSET entries.
func (s *Storage) ExpiredItems(ctx context.Context) ([]sandbox.Sandbox, error) {
	now := time.Now()
	nowMs := float64(now.UnixMilli())

	// Fetch members whose score (EndTime in ms) is <= now, bounded to 256 per cycle.
	expiredMembers, err := s.redisClient.ZRangeByScore(ctx, globalExpirationSet, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   fmt.Sprintf("%v", nowMs),
		Count: expiredItemsBatchSize,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query global expiration index: %w", err)
	}

	if len(expiredMembers) == 0 {
		return nil, nil
	}

	// Group by team for per-team MGET (Redis Cluster slot compatibility).
	type teamEntry struct {
		sandboxIDs []string
		members    []string // original ZSET members, aligned 1:1 with sandboxIDs
	}
	teamSandboxes := make(map[string]*teamEntry)
	for _, member := range expiredMembers {
		teamID, sandboxID, ok := parseExpirationMember(member)
		if !ok {
			logger.L().Warn(ctx, "Invalid expiration index member", zap.String("member", member))

			continue
		}

		entry, ok := teamSandboxes[teamID]
		if !ok {
			entry = &teamEntry{}
			teamSandboxes[teamID] = entry
		}

		entry.sandboxIDs = append(entry.sandboxIDs, sandboxID)
		entry.members = append(entry.members, member)
	}

	pipe := s.redisClient.Pipeline()
	type batchInfo struct {
		cmd     *redis.SliceCmd
		members []string // aligned 1:1 with MGET keys
	}
	var batches []batchInfo
	for teamID, entry := range teamSandboxes {
		keys := make([]string, len(entry.sandboxIDs))
		for i, id := range entry.sandboxIDs {
			keys[i] = getSandboxKey(teamID, id)
		}

		cmd := pipe.MGet(ctx, keys...)
		batches = append(batches, batchInfo{cmd: cmd, members: entry.members})
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("MGET pipeline failed: %w", err)
	}

	// Deserialize and filter; collect stale ZSET members for cleanup.
	var result []sandbox.Sandbox
	var staleMembers []any

	for _, batch := range batches {
		for i, raw := range batch.cmd.Val() {
			// Sandbox key gone but ZSET entry remains — orphaned.
			if raw == nil {
				staleMembers = append(staleMembers, batch.members[i])

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

			// In case that index have failed to be updated
			if !sbx.IsExpired(now) {
				logger.L().Debug(ctx, "ExpiredItems: Sandbox marked as expried in index, but state say otherwise", logger.WithSandboxID(sbx.SandboxID), logger.Time("end_time", sbx.EndTime))

				continue
			}

			// Only evict running sandboxes
			if sbx.State != sandbox.StateRunning {
				if time.Since(sbx.EndTime) <= staleCutoff {
					// Let the current removal finish

					continue
				}

				logger.L().Debug(ctx, "ExpiredItems: Sandbox is in transition state for more than stale cutoff, removing", logger.WithSandboxID(sbx.SandboxID), logger.Time("end_time", sbx.EndTime))
			}

			result = append(result, sbx)
		}
	}

	// Remove orphaned ZSET entries so the set doesn't grow unboundedly.
	if len(staleMembers) > 0 {
		if err := s.redisClient.ZRem(ctx, globalExpirationSet, staleMembers...).Err(); err != nil {
			logger.L().Warn(ctx, "Failed to clean up stale expiration index entries", zap.Error(err), zap.Int("count", len(staleMembers)))
		}
	}

	return result, nil
}
