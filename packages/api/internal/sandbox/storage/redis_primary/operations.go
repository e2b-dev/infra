package redis_primary

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *Storage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	err := s.redisBackend.Add(ctx, sbx)
	if err != nil {
		return err
	}

	err = s.memoryBackend.Add(ctx, sbx)
	if err != nil {
		if !errors.Is(err, sandbox.ErrAlreadyExists) {
			logger.L().Warn(ctx, "failed to shadow sandbox to memory", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
		}
	}

	return nil
}

func (s *Storage) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	sbx, err := s.redisBackend.Get(ctx, teamID, sandboxID)
	if err != nil {
		return s.memoryBackend.Get(ctx, teamID, sandboxID)
	}

	return sbx, nil
}

func (s *Storage) Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	err := s.redisBackend.Remove(ctx, teamID, sandboxID)
	if err != nil {
		return err
	}

	err = s.memoryBackend.Remove(ctx, teamID, sandboxID)
	if err != nil {
		logger.L().Warn(ctx, "failed to remove sandbox from memory", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	return nil
}

func (s *Storage) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox.Sandbox) (sandbox.Sandbox, error)) (sandbox.Sandbox, error) {
	sbx, err := s.redisBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	_, err = s.memoryBackend.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		if !errors.Is(err, sandbox.ErrCannotShortenTTL) {
			logger.L().Warn(ctx, "failed to update sandbox in memory", zap.Error(err), logger.WithSandboxID(sandboxID))
		}
	}

	return sbx, nil
}

func (s *Storage) TeamItems(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	return s.redisBackend.TeamItems(ctx, teamID, states)
}

// Memory backend

func (s *Storage) AllItems(ctx context.Context, states []sandbox.State, options ...sandbox.ItemsOption) ([]sandbox.Sandbox, error) {
	return s.memoryBackend.AllItems(ctx, states, options...)
}

func (s *Storage) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction sandbox.StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	return s.memoryBackend.StartRemoving(ctx, teamID, sandboxID, stateAction)
}

func (s *Storage) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return s.memoryBackend.WaitForStateChange(ctx, teamID, sandboxID)
}

func (s *Storage) Sync(sandboxes []sandbox.Sandbox, nodeID string) []sandbox.Sandbox {
	return s.memoryBackend.Sync(sandboxes, nodeID)
}
