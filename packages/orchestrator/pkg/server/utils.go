//go:build linux

package server

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const sandboxStartWaitPollInterval = 50 * time.Millisecond

// enterSandboxStart admits a sandbox start operation through the single factory
// drain gate and returns a context marked as holding the gate, so the nested
// ResumeSandbox does not re-enter it. The gate is held for the whole handler
// (Create/Checkpoint), which keeps the checkpoint's remove-then-resume atomic
// with respect to drain. Returns Unavailable while the factory is draining.
func (s *Server) enterSandboxStart(ctx context.Context, operation string) (context.Context, func(), error) {
	release, err := s.sandboxFactory.EnterSandboxStart(ctx)
	if errors.Is(err, sandbox.ErrFactoryDraining) {
		logger.L().Info(ctx, "rejecting sandbox operation during orchestrator drain", zap.String("operation", operation))

		return ctx, nil, status.Error(codes.Unavailable, "orchestrator is draining")
	}
	if err != nil {
		return ctx, nil, err
	}

	return sandbox.WithHeldStartGate(ctx), release, nil
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
