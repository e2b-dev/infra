//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/draingate"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *ServerStore) StartDraining(ctx context.Context) {
	if s.drainGate.StartDraining() {
		s.log().Info(ctx, "template server entering drain mode", zap.Int64("active_builds", s.activeBuilds.Load()))
	}
}

func (s *ServerStore) rejectIfDraining(ctx context.Context, operation string) error {
	if s.drainGate.Draining() {
		s.log().Info(ctx, "rejecting template operation during orchestrator drain", zap.String("operation", operation))

		return status.Error(codes.Unavailable, "orchestrator is draining")
	}

	return nil
}

func (s *ServerStore) enterBuildStart(ctx context.Context, operation string) (func(), error) {
	release, err := s.drainGate.Enter()
	if errors.Is(err, draingate.ErrDraining) {
		s.log().Info(ctx, "rejecting template operation during orchestrator drain", zap.String("operation", operation))

		return nil, status.Error(codes.Unavailable, "orchestrator is draining")
	}

	return release, err
}

func (s *ServerStore) waitBuildStarts(ctx context.Context) error {
	s.log().Info(ctx, "waiting for in-flight template build start operations to finish")

	if err := s.drainGate.Wait(ctx); err != nil {
		return fmt.Errorf("waiting for in-flight template build start operations: %w", err)
	}

	s.log().Info(ctx, "in-flight template build start gate acquired")
	s.log().Info(ctx, "in-flight template build start operations finished")

	return nil
}

func (s *ServerStore) log() logger.Logger {
	if s.logger != nil {
		return s.logger
	}

	return logger.L()
}
