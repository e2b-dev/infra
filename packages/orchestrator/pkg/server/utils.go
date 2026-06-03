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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const sandboxStartWaitPollInterval = 50 * time.Millisecond

func (s *Server) rejectIfDraining(ctx context.Context, operation string) error {
	select {
	case <-s.done:
		logger.L().Info(ctx, "rejecting sandbox operation during orchestrator drain", zap.String("operation", operation))

		return status.Error(codes.Unavailable, "orchestrator is draining")
	default:
		return nil
	}
}

func (s *Server) enterSandboxStart(ctx context.Context, operation string) (func(), error) {
	if err := s.rejectIfDraining(ctx, operation); err != nil {
		return nil, err
	}

	s.sandboxStartMu.RLock()
	release := sync.OnceFunc(s.leaveSandboxStart)
	if err := s.rejectIfDraining(ctx, operation); err != nil {
		release()

		return nil, err
	}

	return release, nil
}

func (s *Server) leaveSandboxStart() {
	s.sandboxStartMu.RUnlock()
}

func (s *Server) waitSandboxStarts(ctx context.Context) error {
	logger.L().Info(ctx, "waiting for in-flight sandbox start operations to finish")

	ticker := time.NewTicker(sandboxStartWaitPollInterval)
	defer ticker.Stop()

	for {
		if s.sandboxStartMu.TryLock() {
			logger.L().Info(ctx, "in-flight sandbox start gate acquired")
			s.sandboxStartMu.Unlock()
			logger.L().Info(ctx, "in-flight sandbox start operations finished")
			if s.sandboxFactory != nil {
				return s.sandboxFactory.WaitSandboxStarts(ctx)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for in-flight sandbox start operations: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Server) waitForAcquire(ctx context.Context) error {
	if err := s.rejectIfDraining(ctx, "wait-for-acquire"); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()

	ctx, span := tracer.Start(ctx, "wait-for-acquire")
	defer span.End()

	err := s.startingSandboxes.Acquire(ctx, 1)
	if err != nil {
		telemetry.ReportEvent(ctx, "too many resuming sandboxes on node")

		return status.Errorf(codes.ResourceExhausted, "too many sandboxes resuming on this node, please retry")
	}

	return nil
}
