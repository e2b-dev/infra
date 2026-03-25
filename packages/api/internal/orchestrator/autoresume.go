package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	apisandbox "github.com/e2b-dev/infra/packages/api/internal/sandbox"
	sharedproxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const MaxAutoResumeTransitionRetries = 3

var ErrSandboxStillTransitioning = errors.New(sharedproxygrpc.SandboxStillTransitioningMessage)

func (o *Orchestrator) HandleExistingSandboxAutoResume(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxID string,
	sbx apisandbox.Sandbox,
	transitionWaitBudget time.Duration,
) (string, bool, error) {
	transitionCtx, cancel := context.WithTimeout(ctx, transitionWaitBudget)
	defer cancel()

	attempts := 0

	// Existing sandbox auto-resume state handling:
	// - running: return the current node IP immediately
	// - pausing/snapshotting: wait for the transition, refresh state, and retry
	// - killing: treat as not found
	// - anything else: return internal error
	// - internal transition wait timeout: treat as "still transitioning"
	// - caller cancellation/deadline: propagate the context error
	for {
		switch sbx.State {
		case apisandbox.StatePausing, apisandbox.StateSnapshotting:
			if attempts >= MaxAutoResumeTransitionRetries {
				logger.L().Warn(
					ctx,
					"Sandbox is still transitioning after auto-resume retries",
					logger.WithSandboxID(sandboxID),
					zap.String("state", string(sbx.State)),
					zap.Int("attempts", attempts),
				)

				return "", false, ErrSandboxStillTransitioning
			}

			attempts++
			waitErrMsg := "error waiting for sandbox to pause"
			if sbx.State == apisandbox.StatePausing {
				logger.L().Debug(ctx, "Waiting for sandbox to pause before auto-resume", logger.WithSandboxID(sandboxID), zap.Int("attempt", attempts))
			} else {
				waitErrMsg = "error waiting for sandbox snapshot to finish"
				logger.L().Debug(ctx, "Waiting for sandbox snapshot to finish before auto-resume", logger.WithSandboxID(sandboxID), zap.Int("attempt", attempts))
			}

			err := o.WaitForStateChange(transitionCtx, teamID, sandboxID)
			if err != nil {
				if errors.Is(transitionCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
					logger.L().Warn(
						ctx,
						"Sandbox transition wait timed out during auto-resume",
						logger.WithSandboxID(sandboxID),
						zap.String("state", string(sbx.State)),
						zap.Int("attempt", attempts),
						zap.Duration("budget", transitionWaitBudget),
					)

					return "", false, ErrSandboxStillTransitioning
				}

				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return "", false, err
				}

				return "", false, errors.New(waitErrMsg)
			}

			updatedSandbox, getSandboxErr := o.GetSandbox(ctx, teamID, sandboxID)
			if getSandboxErr == nil {
				sbx = updatedSandbox

				continue
			}
			if errors.Is(getSandboxErr, apisandbox.ErrNotFound) {
				return "", false, nil
			}

			return "", false, fmt.Errorf("failed to refresh sandbox state: %w", getSandboxErr)
		case apisandbox.StateKilling:
			logger.L().Debug(ctx, "Sandbox is being killed, cannot auto-resume", logger.WithSandboxID(sandboxID))

			return "", false, apisandbox.ErrNotFound
		case apisandbox.StateRunning:
			node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
			if node == nil {
				logger.L().Error(
					ctx,
					"Sandbox is running but routing info is not available during auto-resume",
					logger.WithSandboxID(sandboxID),
					logger.WithTeamID(teamID.String()),
					logger.WithNodeID(sbx.NodeID),
					zap.Stringer("cluster_id", sbx.ClusterID),
				)

				return "", false, errors.New("sandbox is running but routing info is not available yet")
			}

			return node.IPAddress, true, nil
		default:
			logger.L().Error(ctx, "Sandbox is in an unknown state during auto-resume", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State)))

			return "", false, errors.New("sandbox is in an unknown state")
		}
	}
}
