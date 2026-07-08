package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	healInterval = 5 * time.Minute

	// healGracePeriod skips recently started sandboxes:
	// this prevents the healer from clearing in-flight Add/Remove
	healGracePeriod = time.Minute

	// healScanBatchSize bounds per-command work (SSCAN page, MGET keys,
	// ZMSCORE members, ZADD members) so teams with many sandboxes can't
	// produce huge single commands/replies that stall Redis or explode service memory
	healScanBatchSize = 256
)

// startHealer restores "sandbox key exists => expiration index member exists".
// A sandbox missing from the global expiration ZSET is never seen by the evictor
// and would otherwise live forever.
//
// Runs on every API pod with jitter; ZADD NX makes concurrent passes idempotent and harmless.
func (s *Storage) startHealer(ctx context.Context) {
	timer := time.NewTimer(jitterBackoff(healInterval))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			healed, err := s.healExpirationIndex(ctx)
			if err != nil {
				logger.L().Warn(ctx, "Expiration index heal pass failed", zap.Error(err))
			}
			if healed > 0 {
				logger.L().Warn(ctx, "Healed sandboxes missing from expiration index", zap.Int("count", healed))
			}

			timer.Reset(jitterBackoff(healInterval))
		}
	}
}

// healerEnabled reports whether the heal pass should run. A nil feature flag
// client (tests) falls back to the flag's default.
func (s *Storage) healerEnabled(ctx context.Context) bool {
	if s.featureFlags == nil {
		return featureflags.ExpirationIndexHealerFlag.Fallback()
	}

	return s.featureFlags.BoolFlag(ctx, featureflags.ExpirationIndexHealerFlag)
}

// healExpirationIndex scans all stored sandboxes and re-adds expiration index
// members missing for live sandbox keys. Returns the number of healed members.
func (s *Storage) healExpirationIndex(ctx context.Context) (int, error) {
	// Re-evaluated every tick: acts as a kill switch without redeploy.
	if !s.healerEnabled(ctx) {
		return 0, nil
	}

	teams, err := s.redisClient.ZRange(ctx, globalTeamsSet, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to list teams from global index: %w", err)
	}

	healed := 0
	for _, teamID := range teams {
		n, err := s.healTeamExpirationIndex(ctx, teamID)
		if err != nil {
			// Isolate per-team failures; other teams still get healed.
			logger.L().Warn(ctx, "Failed to heal team expiration index", zap.Error(err), zap.String("team_id", teamID))

			continue
		}

		healed += n
	}

	return healed, nil
}

func (s *Storage) healTeamExpirationIndex(ctx context.Context, teamID string) (int, error) {
	healed := 0
	var cursor uint64

	for {
		sandboxIDs, next, err := s.redisClient.SScan(ctx, GetSandboxStorageTeamIndexKey(teamID), cursor, "", healScanBatchSize).Result()
		if err != nil {
			return healed, fmt.Errorf("failed to scan team index: %w", err)
		}

		// SSCAN COUNT is a hint, not a cap: split oversized pages so
		// downstream commands stay bounded.
		for start := 0; start < len(sandboxIDs); start += healScanBatchSize {
			end := min(start+healScanBatchSize, len(sandboxIDs))

			n, err := s.healSandboxBatch(ctx, teamID, sandboxIDs[start:end])
			if err != nil {
				return healed, err
			}

			healed += n
		}

		cursor = next
		if cursor == 0 {
			return healed, nil
		}
	}
}

// healSandboxBatch re-adds missing expiration index members for one bounded
// batch of sandbox IDs. Returns the number of healed members.
func (s *Storage) healSandboxBatch(ctx context.Context, teamID string, sandboxIDs []string) (int, error) {
	if len(sandboxIDs) == 0 {
		return 0, nil
	}

	// Per-team MGET: all keys share the team hash tag (cluster slot safe).
	keys := utils.Map(sandboxIDs, func(id string) string { return getSandboxKey(teamID, id) })
	vals, err := s.redisClient.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("MGET failed: %w", err)
	}

	now := time.Now()
	type candidate struct {
		member string
		score  float64
	}
	var candidates []candidate
	for _, raw := range vals {
		str, ok := raw.(string)
		if !ok {
			continue // stale team index entry; TeamItems tolerates these too
		}

		var sbx sandboxtypes.Sandbox
		if err := json.Unmarshal([]byte(str), &sbx); err != nil {
			continue
		}

		if now.Sub(sbx.StartTime) < healGracePeriod {
			continue
		}

		candidates = append(candidates, candidate{
			member: sandboxExpirationMember(sbx),
			score:  float64(sbx.EndTime.UnixMilli()),
		})
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// ZMSCORE returns 0 for absent members; legitimate scores are unix
	// milliseconds and can never be 0.
	members := make([]string, 0, len(candidates))
	for _, c := range candidates {
		members = append(members, c.member)
	}

	scores, err := s.redisClient.ZMScore(ctx, globalExpirationSet, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("ZMSCORE failed: %w", err)
	}

	var missing []redis.Z
	for i, c := range candidates {
		if scores[i] != 0 {
			continue
		}

		missing = append(missing, redis.Z{Score: c.score, Member: c.member})
	}
	if len(missing) == 0 {
		return 0, nil
	}

	// NX: only fill holes. A concurrent Add/Update owns the member's score.
	// A TOCTOU with a concurrent Remove can only plant an orphan member,
	// which ExpiredItems sweeps once its score passes — garbage, never a
	// false eviction (eviction re-checks the stored JSON and re-validates
	// expiry under the lock in StartRemoving).
	if err := s.redisClient.ZAddNX(ctx, globalExpirationSet, missing...).Err(); err != nil {
		return 0, fmt.Errorf("ZADD NX failed: %w", err)
	}

	s.metrics.indexHealed.Add(ctx, int64(len(missing)))

	return len(missing), nil
}
