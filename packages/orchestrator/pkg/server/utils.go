//go:build linux

package server

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/draingate"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// enterSandboxStart admits a new sandbox Create through the start gate and
// returns a release func. It returns Unavailable while the node is draining.
// Checkpoint does not use this: it is a user-initiated snapshot that stays
// allowed during drain and never enters the gate.
func (s *Server) enterSandboxStart(ctx context.Context, operation string) (func(), error) {
	release, err := s.startGate.Enter()
	if errors.Is(err, draingate.ErrDraining) {
		logger.L().Info(ctx, "rejecting sandbox operation during orchestrator drain", zap.String("operation", operation))

		return nil, status.Error(codes.Unavailable, "orchestrator is draining")
	}
	if err != nil {
		return nil, err
	}

	return release, nil
}

func (s *Server) waitForAcquire(ctx context.Context) error {
	// Callers already passed enterSandboxStart; do not reject admitted starts
	// during graceful drain because DrainSandboxes waits for them before the
	// start gate finishes draining.
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
