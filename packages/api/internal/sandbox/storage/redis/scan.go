package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// sandboxScanBatchSize bounds per-command work (SSCAN page, MGET keys) so
// teams with many sandboxes can't produce huge single commands/replies that
// stall Redis or explode service memory.
const sandboxScanBatchSize = 256

// forEachSandboxBatch scans every team's sandbox index and invokes fn with
// bounded batches of unmarshalled records (all states, stale index entries
// skipped). Per-team failures — including errors returned by fn — are
// isolated: the failing team is skipped and the remaining teams are still
// scanned. Only a failure to list the teams themselves is returned.
func (s *Storage) forEachSandboxBatch(ctx context.Context, fn func(teamID string, batch []sandboxtypes.Sandbox) error) error {
	teams, err := s.redisClient.ZRange(ctx, globalTeamsSet, 0, -1).Result()
	if err != nil {
		return fmt.Errorf("failed to list teams from global index: %w", err)
	}

	for _, teamID := range teams {
		if err := s.forEachTeamSandboxBatch(ctx, teamID, fn); err != nil {
			logger.L().Warn(ctx, "Failed to scan team sandboxes, skipping team",
				zap.Error(err),
				logger.WithTeamID(teamID),
			)

			continue
		}
	}

	return nil
}

// forEachTeamSandboxBatch pages through one team's sandbox index with SSCAN
// and feeds fn bounded batches fetched via MGET.
func (s *Storage) forEachTeamSandboxBatch(ctx context.Context, teamID string, fn func(teamID string, batch []sandboxtypes.Sandbox) error) error {
	var cursor uint64

	for {
		sandboxIDs, next, err := s.redisClient.SScan(ctx, GetSandboxStorageTeamIndexKey(teamID), cursor, "", sandboxScanBatchSize).Result()
		if err != nil {
			return fmt.Errorf("failed to scan team index: %w", err)
		}

		// SSCAN COUNT is a hint, not a cap: split oversized pages so
		// downstream MGET commands stay bounded.
		for start := 0; start < len(sandboxIDs); start += sandboxScanBatchSize {
			end := min(start+sandboxScanBatchSize, len(sandboxIDs))

			batch, err := s.fetchSandboxBatch(ctx, teamID, sandboxIDs[start:end])
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				continue
			}

			if err := fn(teamID, batch); err != nil {
				return err
			}
		}

		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// fetchSandboxBatch MGETs one bounded batch of sandbox records. Stale index
// entries (key already deleted) and unparseable records are skipped.
func (s *Storage) fetchSandboxBatch(ctx context.Context, teamID string, sandboxIDs []string) ([]sandboxtypes.Sandbox, error) {
	if len(sandboxIDs) == 0 {
		return nil, nil
	}

	// Per-team MGET: all keys share the team hash tag (cluster slot safe).
	keys := utils.Map(sandboxIDs, func(id string) string { return getSandboxKey(teamID, id) })
	vals, err := s.redisClient.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("MGET failed: %w", err)
	}

	out := make([]sandboxtypes.Sandbox, 0, len(vals))
	for _, raw := range vals {
		str, ok := raw.(string)
		if !ok {
			continue // stale team index entry; TeamItems tolerates these too
		}

		var sbx sandboxtypes.Sandbox
		if err := json.Unmarshal([]byte(str), &sbx); err != nil {
			logger.L().Error(ctx, "Failed to unmarshal sandbox during scan", zap.Error(err))

			continue
		}

		out = append(out, sbx)
	}

	return out, nil
}

// AllRunningItems returns every running sandbox in the store across all
// teams, scanned in bounded batches. Used by the dead-node sweep.
func (s *Storage) AllRunningItems(ctx context.Context) ([]sandboxtypes.Sandbox, error) {
	var out []sandboxtypes.Sandbox

	err := s.forEachSandboxBatch(ctx, func(_ string, batch []sandboxtypes.Sandbox) error {
		for _, sbx := range batch {
			if sbx.State != sandboxtypes.StateRunning {
				continue
			}

			out = append(out, sbx)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}
