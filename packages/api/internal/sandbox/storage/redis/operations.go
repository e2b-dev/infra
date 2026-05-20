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
	teamKey := GetSandboxStorageTeamIndexKey(sbx.TeamID.String())

	// Add to the index before adding to the cache, so there's no possibility of leaking
	// Index by EndTime so ExpiredItems can use ZRANGEBYSCORE instead of scanning all sandboxes.
	zaddCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	zaddErr := s.redisClient.ZAdd(zaddCtx, globalExpirationSet, redis.Z{
		Score:  float64(sbx.EndTime.UnixMilli()),
		Member: expirationMember(sbx.TeamID.String(), sbx.SandboxID),
	}).Err()
	cancel()
	if zaddErr != nil {
		return fmt.Errorf("failed to add sandbox to global expiration index: %w", zaddErr)
	}

	// Execute Lua script for atomic SET + SADD
	scriptCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	err = addSandboxScript.Run(scriptCtx, s.redisClient, []string{key, teamKey}, data, sbx.SandboxID).Err()
	cancel()
	if err != nil {
		return fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	// We can't set the globalTeamsSet in Lua script as they can be in different shards
	teamsCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	teamsErr := s.redisClient.ZAdd(teamsCtx, globalTeamsSet, redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: sbx.TeamID.String(),
	}).Err()
	cancel()
	if teamsErr != nil {
		logger.L().Warn(ctx, "failed to add team to global teams index", zap.Error(teamsErr), logger.WithSandboxID(sbx.SandboxID))
	}

	return nil
}

// Get retrieves a sandbox from Redis
func (s *Storage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	key := getSandboxKey(teamID.String(), sandboxID)
	getCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	data, err := s.redisClient.Get(getCtx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
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
	teamKey := GetSandboxStorageTeamIndexKey(teamID.String())

	lockKey := redis_utils.GetLockKey(key)
	lock, err := s.locker.Obtain(ctx, lockKey, lockTimeout)
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
	scriptCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	err = removeSandboxScript.Run(scriptCtx, s.redisClient, []string{key, teamKey}, sandboxID).Err()
	cancel()
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from Redis: %w", err)
	}

	// Clean up from the global expiration index.
	// Do it after the removal to prevent leaking expired sandboxes.
	zremCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	zremErr := s.redisClient.ZRem(zremCtx, globalExpirationSet, expirationMember(teamID.String(), sandboxID)).Err()
	cancel()
	if zremErr != nil {
		logger.L().Warn(ctx, "Failed to remove sandbox from global expiration index", zap.Error(zremErr), logger.WithSandboxID(sandboxID))
	}

	return nil
}

// TeamItems retrieves sandboxes for a specific team, filtered by states and options
func (s *Storage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	// Get sandbox IDs from team index
	teamKey := GetSandboxStorageTeamIndexKey(teamID.String())
	smembersCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	sandboxIDs, err := s.redisClient.SMembers(smembersCtx, teamKey).Result()
	cancel()
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

	mgetCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	results, err := s.redisClient.MGet(mgetCtx, keys...).Result()
	cancel()
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

	lockKey := redis_utils.GetLockKey(key)
	lock, err := s.locker.Obtain(ctx, lockKey, lockTimeout)
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
	getCtx, getCancel := context.WithTimeout(ctx, redisOpTimeout)
	data, err := s.redisClient.Get(getCtx, key).Bytes()
	getCancel()
	if errors.Is(err, redis.Nil) {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %q: %w", sandboxID, sandbox.ErrNotFound)
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
	setCtx, setCancel := context.WithTimeout(ctx, redisOpTimeout)
	err = s.redisClient.Set(setCtx, key, newData, redis.KeepTTL).Err()
	setCancel()
	if err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("failed to store sandbox in Redis: %w", err)
	}

	// Re-score the expiration index if EndTime changed.
	if !updatedSbx.EndTime.Equal(sbx.EndTime) {
		zaddCtx, zaddCancel := context.WithTimeout(ctx, redisOpTimeout)
		zaddErr := s.redisClient.ZAdd(zaddCtx, globalExpirationSet, redis.Z{
			Score:  float64(updatedSbx.EndTime.UnixMilli()),
			Member: expirationMember(teamID.String(), sandboxID),
		}).Err()
		zaddCancel()
		if zaddErr != nil {
			return sandbox.Sandbox{}, fmt.Errorf("failed to update sandbox in global expiration index: %w", zaddErr)
		}
	}

	return updatedSbx, nil
}

func (s *Storage) TeamsWithSandboxCount(ctx context.Context) (map[uuid.UUID]int64, error) {
	zrangeCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	members, err := s.redisClient.ZRangeWithScores(zrangeCtx, globalTeamsSet, 0, -1).Result()
	cancel()
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
		cmd := pipe.SCard(ctx, GetSandboxStorageTeamIndexKey(raw))
		entries = append(entries, teamEntry{id: id, score: m.Score, cmd: cmd})
	}

	if len(entries) == 0 {
		return map[uuid.UUID]int64{}, nil
	}

	pipeCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	_, err = pipe.Exec(pipeCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("SCARD pipeline failed: %w", err)
	}

	now := time.Now().Unix()
	cutoff := now - int64(sandbox.StaleCutoff.Seconds())

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
		zremCtx, zremCancel := context.WithTimeout(ctx, redisOpTimeout)
		zremErr := s.redisClient.ZRem(zremCtx, globalTeamsSet, stale...).Err()
		zremCancel()
		if zremErr != nil {
			logger.L().Warn(ctx, "Failed to prune stale teams from global index", zap.Error(zremErr), zap.Int("count", len(stale)))
		}
	}

	return teams, nil
}
