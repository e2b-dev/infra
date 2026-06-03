//go:build linux

package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const buildStartWaitPollInterval = 50 * time.Millisecond

func (s *ServerStore) StartDraining(ctx context.Context) {
	s.drainOnce.Do(func() {
		if s.drainDone == nil {
			s.drainDone = make(chan struct{})
		}

		s.log().Info(ctx, "template server entering drain mode", zap.Int64("active_builds", s.activeBuilds.Load()))
		close(s.drainDone)
	})
}

func (s *ServerStore) rejectIfDraining(ctx context.Context, operation string) error {
	select {
	case <-s.drainDone:
		s.log().Info(ctx, "rejecting template operation during orchestrator drain", zap.String("operation", operation))

		return status.Error(codes.Unavailable, "orchestrator is draining")
	default:
		return nil
	}
}

func (s *ServerStore) enterBuildStart(ctx context.Context, operation string) (func(), error) {
	if err := s.rejectIfDraining(ctx, operation); err != nil {
		return nil, err
	}

	s.buildStartMu.RLock()
	release := sync.OnceFunc(s.leaveBuildStart)
	if err := s.rejectIfDraining(ctx, operation); err != nil {
		release()

		return nil, err
	}

	return release, nil
}

func (s *ServerStore) leaveBuildStart() {
	s.buildStartMu.RUnlock()
}

func (s *ServerStore) waitBuildStarts(ctx context.Context) error {
	s.log().Info(ctx, "waiting for in-flight template build start operations to finish")

	ticker := time.NewTicker(buildStartWaitPollInterval)
	defer ticker.Stop()

	for {
		if s.buildStartMu.TryLock() {
			s.log().Info(ctx, "in-flight template build start gate acquired")
			s.buildStartMu.Unlock()
			s.log().Info(ctx, "in-flight template build start operations finished")

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for in-flight template build start operations: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *ServerStore) log() logger.Logger {
	if s.logger != nil {
		return s.logger
	}

	return logger.L()
}
