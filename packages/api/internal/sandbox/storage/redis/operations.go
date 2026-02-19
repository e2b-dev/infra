package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Add stores a sandbox in Redis atomically with its team index entry.
func (s *Storage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	data, err := json.Marshal(sbx)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	key := getSandboxKey(sbx.TeamID.String(), sbx.SandboxID)
	teamKey := getTeamIndexKey(sbx.TeamID.String())

	// Execute Lua script for atomic SET + SADD
	err = addSandboxScript.Run(ctx, s.redisClient, []string{key, teamKey}, data, sbx.SandboxID).Err()
	if err != nil {
		return fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	// We can't set the globalTeamsSet in Lua script as they can be in different shards
	if err := s.redisClient.ZAdd(ctx, globalTeamsSet, redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: sbx.TeamID.String(),
	}).Err(); err != nil {
		return fmt.Errorf("failed to add team to global teams index: %w", err)
	}

	// Index by EndTime so ExpiredItems can use ZRANGEBYSCORE instead of scanning all sandboxes.
	if err := s.redisClient.ZAdd(ctx, globalExpirationSet, redis.Z{
		Score:  float64(sbx.EndTime.UnixMilli()),
		Member: expirationMember(sbx.TeamID.String(), sbx.SandboxID),
	}).Err(); err != nil {
		return fmt.Errorf("failed to add sandbox to global expiration index: %w", err)
	}

	return nil
}

// Get retrieves a sandbox from Redis
func (s *Storage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	key := getSandboxKey(teamID.String(), sandboxID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{SandboxID: sandboxID}
	}
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to get sandbox from Redis: %w", err)
	}

	var sbx sandbox.Sandbox
	err = json.Unmarshal(data, &sbx)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to unmarshal sandbox: %w", err)
	}

	return sbx, nil
}

// Remove deletes a sandbox from Redis atomically with its team index entry.
func (s *Storage) Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	key := getSandboxKey(teamID.String(), sandboxID)
	teamKey := getTeamIndexKey(teamID.String())

	lock, err := s.lockService.Obtain(ctx, redis_utils.GetLockKey(key), lockTimeout, s.lockOption)
	if err != nil {
		return fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "Failed to release lock", zap.Error(err))
		}
	}()

	// Execute Lua script for atomic DEL + SREM
	err = removeSandboxScript.Run(ctx, s.redisClient, []string{key, teamKey}, sandboxID).Err()
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from Redis: %w", err)
	}

	// Clean up from the global expiration index.
	if err := s.redisClient.ZRem(ctx, globalExpirationSet, expirationMember(teamID.String(), sandboxID)).Err(); err != nil {
		logger.L().Warn(ctx, "Failed to remove sandbox from global expiration index", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	return nil
}

// TeamItems retrieves sandboxes for a specific team, filtered by states and options
func (s *Storage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	// Get sandbox IDs from team index
	teamKey := getTeamIndexKey(teamID.String())
	sandboxIDs, err := s.redisClient.SMembers(ctx, teamKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox IDs from team index: %w", err)
	}

	if len(sandboxIDs) == 0 {
		return []sandbox.Sandbox{}, nil
	}

	// Build keys and batch fetch with MGET
	team := teamID.String()
	keys := utils.Map(sandboxIDs, func(id string) string {
		return getSandboxKey(team, id)
	})

	results, err := s.redisClient.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get sandboxes from Redis: %w", err)
	}

	// Deserialize and filter
	var sandboxes []sandbox.Sandbox
	for _, rawResult := range results {
		if rawResult == nil {
			continue // Stale index entry - sandbox was deleted
		}

		var sbx sandbox.Sandbox
		result, ok := rawResult.(string)
		if !ok {
			logger.L().Error(ctx, "Invalid sandbox data type in Redis")

			continue
		}

		if err := json.Unmarshal([]byte(result), &sbx); err != nil {
			logger.L().Error(ctx, "Failed to unmarshal sandbox", zap.Error(err))

			continue
		}

		// Filter by state if states are specified
		if len(states) > 0 && !slices.Contains(states, sbx.State) {
			continue
		}

		sandboxes = append(sandboxes, sbx)
	}

	return sandboxes, nil
}

// Update modifies a sandbox atomically
func (s *Storage) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	key := getSandboxKey(teamID.String(), sandboxID)

	lock, err := s.lockService.Obtain(ctx, redis_utils.GetLockKey(key), lockTimeout, s.lockOption)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to obtain lock: %w", err)
	}

	defer func() {
		err := lock.Release(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "Failed to release lock", zap.Error(err))
		}
	}()

	// Get current value
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{SandboxID: sandboxID}
	}
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	var sbx sandbox.Sandbox
	err = json.Unmarshal(data, &sbx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	// Apply update
	updatedSbx, err := updateFunc(sbx)
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to update sandbox: %w", err)
	}

	// Serialize updated sandbox
	newData, err := json.Marshal(updatedSbx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	// Execute transaction
	err = s.redisClient.Set(ctx, key, newData, redis.KeepTTL).Err()
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	// Re-score the expiration index if EndTime changed.
	if !updatedSbx.EndTime.Equal(sbx.EndTime) {
		if err := s.redisClient.ZAdd(ctx, globalExpirationSet, redis.Z{
			Score:  float64(updatedSbx.EndTime.UnixMilli()),
			Member: expirationMember(teamID.String(), sandboxID),
		}).Err(); err != nil {
			logger.L().Warn(ctx, "Failed to update sandbox in global expiration index", zap.Error(err), logger.WithSandboxID(sandboxID))
		}
	}

	return updatedSbx, nil
}

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

// staleCutoff is how long a team entry must be idle (no Add calls) before it
// can be pruned from the global teams ZSET when its sandbox count is zero.
// This prevents races where a Remove sees SCARD==0 right before an Add.
const staleCutoff = time.Hour

func (s *Storage) TeamsWithSandboxCount(ctx context.Context) (map[uuid.UUID]int64, error) {
	members, err := s.redisClient.ZRangeWithScores(ctx, globalTeamsSet, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get teams from global index: %w", err)
	}

	// Pipeline SCARD per team index key to get counts and filter stale entries
	type teamEntry struct {
		id    uuid.UUID
		score float64
		cmd   *redis.IntCmd
	}

	pipe := s.redisClient.Pipeline()
	entries := make([]teamEntry, 0, len(members))
	for _, m := range members {
		raw, ok := m.Member.(string)
		if !ok {
			continue
		}
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			logger.L().Warn(ctx, "Failed to parse team ID from global teams index", zap.Error(parseErr), zap.String("raw", raw))

			continue
		}
		cmd := pipe.SCard(ctx, getTeamIndexKey(raw))
		entries = append(entries, teamEntry{id: id, score: m.Score, cmd: cmd})
	}

	if len(entries) == 0 {
		return map[uuid.UUID]int64{}, nil
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("SCARD pipeline failed: %w", err)
	}

	now := time.Now().Unix()
	cutoff := now - int64(staleCutoff.Seconds())

	teams := make(map[uuid.UUID]int64, len(entries))
	var stale []any
	for _, e := range entries {
		if count := e.cmd.Val(); count > 0 {
			teams[e.id] = count
		} else if int64(e.score) < cutoff {
			// Only prune if the entry is old enough — a fresh score means
			// an Add happened recently and SCARD==0 may be a transient race.
			stale = append(stale, e.id.String())
		}
	}

	// Prune stale entries from the global teams index
	if len(stale) > 0 {
		if err := s.redisClient.ZRem(ctx, globalTeamsSet, stale...).Err(); err != nil {
			logger.L().Warn(ctx, "Failed to prune stale teams from global index", zap.Error(err), zap.Int("count", len(stale)))
		}
	}

	return teams, nil
}
