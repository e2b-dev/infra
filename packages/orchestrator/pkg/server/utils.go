//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/draingate"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const sandboxStartWaitPollInterval = 50 * time.Millisecond

func (s *Server) enterSandboxStart(ctx context.Context, operation string) (func(), error) {
	release, err := s.drainGate.Enter()
	if errors.Is(err, draingate.ErrDraining) {
		logger.L().Info(ctx, "rejecting sandbox operation during orchestrator drain", zap.String("operation", operation))

		return nil, status.Error(codes.Unavailable, "orchestrator is draining")
	}

	return release, err
}

func (s *Server) waitServerSandboxStarts(ctx context.Context) error {
	logger.L().Info(ctx, "waiting for in-flight sandbox start operations to finish")

	if err := s.drainGate.Wait(ctx); err != nil {
		return fmt.Errorf("waiting for in-flight sandbox start operations: %w", err)
	}

	logger.L().Info(ctx, "in-flight sandbox start operations finished")

	return nil
}

func (s *Server) waitSandboxStarts(ctx context.Context) error {
	if err := s.waitServerSandboxStarts(ctx); err != nil {
		return err
	}

	if s.sandboxFactory != nil {
		return s.sandboxFactory.WaitSandboxStarts(ctx)
	}

	return nil
}

func (s *Server) waitForAcquire(ctx context.Context) error {
	// Callers already passed enterSandboxStart; do not reject admitted starts
	// during graceful drain because DrainSandboxes waits for them before factory drain.
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
