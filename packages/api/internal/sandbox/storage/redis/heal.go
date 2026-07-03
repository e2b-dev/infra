package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	healInterval = 5 * time.Minute

	// healGracePeriod skips recently started sandboxes:
	// this prevents the healer from clearing in-flight Add/Remove
	healGracePeriod = time.Minute
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

// healExpirationIndex scans all stored sandboxes and re-adds expiration index
// members missing for live sandbox keys. Returns the number of healed members.
func (s *Storage) healExpirationIndex(ctx context.Context) (int, error) {
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
	sandboxIDs, err := s.redisClient.SMembers(ctx, GetSandboxStorageTeamIndexKey(teamID)).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to read team index: %w", err)
	}
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
		member       string
		legacyMember string
		score        float64
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
			member:       sandboxExpirationMember(sbx),
			legacyMember: legacyExpirationMember(teamID, sbx.SandboxID),
			score:        float64(sbx.EndTime.UnixMilli()),
		})
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// A sandbox is indexed if either its execution-scoped member or the
	// legacy member (written by old pods during rolling deploys) exists.
	// ZMSCORE returns 0 for absent members; legitimate scores are unix
	// milliseconds and can never be 0.
	members := make([]string, 0, len(candidates)*2)
	for _, c := range candidates {
		members = append(members, c.member, c.legacyMember)
	}

	scores, err := s.redisClient.ZMScore(ctx, globalExpirationSet, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("ZMSCORE failed: %w", err)
	}

	var missing []redis.Z
	for i, c := range candidates {
		if scores[2*i] != 0 || scores[2*i+1] != 0 {
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
