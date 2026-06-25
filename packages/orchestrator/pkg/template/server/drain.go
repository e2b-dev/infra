//go:build linux

package server

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *ServerStore) StartDraining(ctx context.Context) {
	if s.drainGate.StartDraining() {
		s.log().Info(ctx, "template server entering drain mode", zap.Int64("active_builds", s.activeBuilds.Load()))
	}
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
