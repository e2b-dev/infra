package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (o *Orchestrator) RemoveSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox")
	defer span.End()

	sbx, alreadyDone, finish, err := o.sandboxStore.StartRemoving(ctx, teamID, sandboxID, opts)
	if err != nil {
		// For eviction, propagate all errors to the evictor.
		if opts.Eviction {
			return err
		}

		switch opts.Action {
		case sandbox.StateActionKill:
			if errors.Is(err, sandbox.ErrNotFound) {
				logger.L().Info(ctx, "Sandbox not found, already removed", logger.WithSandboxID(sandboxID))

				return ErrSandboxNotFound
			}

			switch sbx.State {
			case sandbox.StateKilling:
				logger.L().Info(ctx, "Sandbox is already killed", logger.WithSandboxID(sandboxID))

				return nil
			default: // It shouldn't happen the sandbox ended in paused state
				logger.L().Error(ctx, "Error killing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

				return ErrSandboxOperationFailed
			}
		case sandbox.StateActionPause:
			if errors.Is(err, sandbox.ErrNotFound) {
				logger.L().Info(ctx, "Sandbox not found for pause", logger.WithSandboxID(sandboxID))

				return ErrSandboxNotFound
			}

			var transErr *sandbox.InvalidStateTransitionError
			if errors.As(err, &transErr) {
				if transErr.CurrentState == sandbox.StateKilling {
					logger.L().Info(ctx, "Sandbox is already killed", logger.WithSandboxID(sandboxID))

					return ErrSandboxNotFound
				}

				return fmt.Errorf("sandbox is in '%s' state: %w", transErr.CurrentState, err)
			}

			logger.L().Error(ctx, "Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

			return ErrSandboxOperationFailed
		default:
			logger.L().Error(ctx, "Invalid state action", logger.WithSandboxID(sandboxID), zap.String("state_action", opts.Action.Name))

			return ErrSandboxOperationFailed
		}
	}
	defer func() {
		finish(ctx, err)
	}()

	if alreadyDone {
		logger.L().Info(ctx, "Sandbox was already in the process of being removed", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State)))

		return nil
	}

	defer func() { go o.analyticsRemove(context.WithoutCancel(ctx), sbx, opts.Action) }()
	defer o.sandboxStore.Remove(ctx, teamID, sandboxID)
	err = o.removeSandboxFromNode(ctx, sbx, opts.Action)
	if err != nil {
		logger.L().Error(ctx, "Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))

		return ErrSandboxOperationFailed
	}

	return nil
}

func (o *Orchestrator) removeSandboxFromNode(ctx context.Context, sbx sandbox.Sandbox, stateAction sandbox.StateAction) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox-from-node")
	defer span.End()

	node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
	if node == nil {
		logger.L().Error(ctx, "failed to get node", logger.WithNodeID(sbx.NodeID))

		return fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	// Only remove from routing table if the node is managed by Nomad
	// For remote cluster nodes we are using gPRC metadata for routing registration instead
	if node.IsNomadManaged() || env.IsLocal() {
		// Remove the sandbox resources after the sandbox is deleted
		err := o.routingCatalog.DeleteSandbox(ctx, sbx.SandboxID, sbx.ExecutionID)
		if err != nil {
			logger.L().Error(ctx, "error removing routing record from catalog", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
		}
	}

	sbxlogger.I(sbx).Debug(ctx, "Removing sandbox",
		zap.Bool("auto_pause", sbx.AutoPause),
		zap.String("state_action", stateAction.Name),
	)

	switch stateAction {
	case sandbox.StateActionPause:
		err := o.pauseSandbox(ctx, node, sbx)
		if err != nil {
			if dberrors.IsForeignKeyViolation(err) {
				killErr := o.killSandboxOnNode(ctx, node, sbx)
				logger.L().Error(ctx, "Pause failed due to missing base template, killed sandbox as fallback",
					logger.WithSandboxID(sbx.SandboxID),
					zap.String("base_template_id", sbx.BaseTemplateID),
					zap.NamedError("pause_error", err),
					zap.NamedError("kill_error", killErr),
				)

				return fmt.Errorf("failed to pause sandbox '%s': base template no longer exists: %w", sbx.SandboxID, err)
			}

			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sbx.SandboxID, err)
		}

		return nil
	case sandbox.StateActionKill:
		return o.killSandboxOnNode(ctx, node, sbx)
	}

	return nil
}

func (o *Orchestrator) killSandboxOnNode(ctx context.Context, node *nodemanager.Node, sbx sandbox.Sandbox) error {
	req := &orchestrator.SandboxDeleteRequest{SandboxId: sbx.SandboxID}

	client, ctx := node.GetSandboxDeleteCtx(ctx, sbx.SandboxID, sbx.ExecutionID)
	_, err := client.Sandbox.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox '%s': %w", sbx.SandboxID, err)
	}

	return nil
}
