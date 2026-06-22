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

func (s *ServerStore) enterTemplateOperationStart(ctx context.Context, operation string) (func(), error) {
	release, err := s.drainGate.Enter()
	if errors.Is(err, draingate.ErrDraining) {
		s.log().Info(ctx, "rejecting template operation during orchestrator drain", zap.String("operation", operation))

		return nil, status.Error(codes.Unavailable, "orchestrator is draining")
	}

	return release, err
}

func (s *ServerStore) waitTemplateOperationStarts(ctx context.Context) error {
	s.log().Info(ctx, "waiting for in-flight template operation starts to finish")

	if err := s.drainGate.Wait(ctx); err != nil {
		return fmt.Errorf("waiting for in-flight template operation starts: %w", err)
	}

	s.log().Info(ctx, "in-flight template operation starts finished")

	return nil
}

func (s *ServerStore) log() logger.Logger {
	if s.logger != nil {
		return s.logger
	}

	return logger.L()
}
