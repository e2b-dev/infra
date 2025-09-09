package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (s *Store) Add(ctx context.Context, sandbox *Sandbox, newlyCreated bool) error {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
		logger.WithSandboxID(sandbox.SandboxID),
	)

	if sandbox.SandboxID == "" {
		return fmt.Errorf("sandbox is missing sandbox ID")
	}

	if sandbox.TeamID == uuid.Nil {
		return fmt.Errorf("sandbox %s is missing team ID", sandbox.SandboxID)
	}

	if sandbox.ClientID == "" {
		return fmt.Errorf("sandbox %s is missing client ID", sandbox.ClientID)
	}

	if sandbox.TemplateID == "" {
		return fmt.Errorf("sandbox %s is missing env ID", sandbox.TemplateID)
	}

	endTime := sandbox.EndTime

	if sandbox.StartTime.IsZero() || endTime.IsZero() || sandbox.StartTime.After(endTime) {
		return fmt.Errorf("sandbox %s has invalid start(%s)/end(%s) times", sandbox.SandboxID, sandbox.StartTime, endTime)
	}

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	err := s.backend.Add(ctx, sandbox, newlyCreated)
	if err != nil {
		return fmt.Errorf("failed to add sandbox to cache: %w", err)
	}

	for _, callback := range s.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range s.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}

	return nil
}

// Exists Check if the sandbox exists in the cache or is being evicted.
func (s *Store) Exists(ctx context.Context, sandboxID string) bool {
	return s.backend.Exists(ctx, sandboxID)
}

// Get the item from the cache.
func (s *Store) Get(ctx context.Context, sandboxID string, includeEvicting bool) (*Sandbox, error) {
	return s.backend.Get(ctx, sandboxID, includeEvicting)
}

func (s *Store) Remove(ctx context.Context, sandboxID string, removeType RemoveType) (err error) {
	sbx, err := s.backend.MarkRemoving(ctx, sandboxID, removeType)
	if err != nil {
		return fmt.Errorf("failed to mark sandbox %s as removing: %w", sandboxID, err)
	}

	zap.L().Debug("Removing sandbox from cache",
		zap.Time("start_time", sbx.StartTime),
		zap.Time("end_time", sbx.EndTime),
		logger.WithSandboxID(sbx.SandboxID),
	)

	for _, callback := range s.removeAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sbx, removeType)
	}

	// Remove from the store
	err = s.backend.Remove(ctx, sandboxID, removeType)
	if err != nil {
		return fmt.Errorf("error removing sandbox \"%s\": %w", sandboxID, err)
	}

	return nil
}

func (s *Store) Items(ctx context.Context, teamID *uuid.UUID) []*Sandbox {
	return s.backend.Items(ctx, teamID)
}

func (s *Store) ExpiredItems(ctx context.Context) []*Sandbox {
	return s.backend.ExpiredItems(ctx)
}

func (s *Store) ItemsByState(ctx context.Context, teamID *uuid.UUID, states []State) map[State][]*Sandbox {
	return s.backend.ItemsByState(ctx, teamID, states)
}

func (s *Store) Len(ctx context.Context, teamID *uuid.UUID) int {
	return len(s.Items(ctx, teamID))
}

// KeepAliveFor the sandbox's expiration timer.
func (s *Store) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) (*Sandbox, error) {
	sbx, err := s.Get(ctx, sandboxID, false)
	if err != nil {
		return nil, ErrSandboxNotFound
	}

	now := time.Now()

	endTime := sbx.EndTime
	if !allowShorter && endTime.After(now.Add(duration)) {
		return sbx, nil
	}

	if (time.Since(sbx.StartTime)) > sbx.MaxInstanceLength {
		return nil, ErrMaxSandboxUptimeReached
	} else {
		maxAllowedTTL := GetMaxAllowedTTL(now, sbx.StartTime, duration, sbx.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		sbx.EndTime = newEndTime

		err = s.backend.Update(ctx, sbx)
		if err != nil {
			zap.L().Error("Failed to update sandbox in store",
				zap.String("sandbox_id", sandboxID),
				zap.Error(err))
			return nil, fmt.Errorf("failed to update sandbox in store: %w", err)
		}
	}

	return sbx, nil
}

func (s *Store) WaitForStop(ctx context.Context, sandboxID string) error {
	return s.backend.WaitForStop(ctx, sandboxID)
}

func (s *Store) Reserve(ctx context.Context, sandboxID string, team uuid.UUID, limit int64) (release func(), err error) {
	return s.backend.Reserve(ctx, sandboxID, team, limit)
}
