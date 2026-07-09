package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gogo/status"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"

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
				logger.L().Info(ctx, "Sandbox not found, already removed",
					logger.WithSandboxID(sandboxID),
					zap.String("kill_reason", opts.Reason.String()),
				)

				return ErrSandboxNotFound
			}

			switch sbx.State {
			case sandbox.StateKilling:
				logger.L().Info(ctx, "Sandbox is already killed",
					logger.WithSandboxID(sandboxID),
					zap.String("kill_reason", opts.Reason.String()),
				)

				return nil
			default: // It shouldn't happen the sandbox ended in paused state
				logger.L().Error(ctx, "Error killing sandbox",
					zap.Error(err),
					logger.WithSandboxID(sandboxID),
					zap.String("kill_reason", opts.Reason.String()),
				)

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
		finish(context.WithoutCancel(ctx), err)
	}()

	if alreadyDone {
		logger.L().Info(ctx, "Sandbox was already in the process of being removed", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State)))

		if time.Since(sbx.EndTime) > sandbox.StaleCutoff && opts.Action.Effect == sandbox.TransitionExpires {
			o.sandboxStore.Remove(context.WithoutCancel(ctx), teamID, sandboxID)
			go o.analyticsRemove(context.WithoutCancel(ctx), sbx, opts.Action)
		}

		return nil
	}

	defer func() { go o.analyticsRemove(context.WithoutCancel(ctx), sbx, opts.Action) }()
	// Once we start the removal process, we want to make sure it gets removed from the store
	defer o.sandboxStore.Remove(context.WithoutCancel(ctx), teamID, sandboxID)
	err = o.removeSandboxFromNode(ctx, sbx, opts.Action, opts.Reason, opts.FilesystemOnly)
	if err != nil {
		fields := []zap.Field{
			zap.String("state_action", opts.Action.Name),
			zap.Error(err),
			logger.WithSandboxID(sbx.SandboxID),
		}
		if opts.Action == sandbox.StateActionKill {
			fields = append(fields, zap.String("kill_reason", opts.Reason.String()))
		}

		logger.L().Error(ctx, "Error removing sandbox", fields...)

		return ErrSandboxOperationFailed
	}

	return nil
}

func (o *Orchestrator) removeSandboxFromNode(
	ctx context.Context,
	sbx sandbox.Sandbox,
	stateAction sandbox.StateAction,
	reason sandbox.KillReason,
	filesystemOnly bool,
) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox-from-node")
	defer span.End()

	node := o.getOrConnectNode(ctx, sbx.ClusterID, sbx.NodeID)
	if node == nil {
		fields := []zap.Field{
			logger.WithNodeID(sbx.NodeID),
		}
		if stateAction == sandbox.StateActionKill {
			fields = append(fields, zap.String("kill_reason", reason.String()))
		}

		logger.L().Error(ctx, "failed to get node", fields...)

		return fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	// Only remove from routing table if the node is managed by Nomad
	// For remote cluster nodes we are using gPRC metadata for routing registration instead
	if node.IsNomadManaged() || env.IsLocal() {
		// Remove the sandbox resources after the sandbox is deleted
		err := o.routingCatalog.DeleteSandbox(ctx, sbx.SandboxID, sbx.ExecutionID)
		if err != nil {
			fields := []zap.Field{
				zap.Error(err),
				logger.WithSandboxID(sbx.SandboxID),
			}
			if stateAction == sandbox.StateActionKill {
				fields = append(fields, zap.String("kill_reason", reason.String()))
			}

			logger.L().Error(ctx, "error removing routing record from catalog", fields...)
		}
	}

	sbxlogger.I(sbx).Debug(ctx, "Removing sandbox",
		zap.Bool("auto_pause", sbx.AutoPause),
		zap.String("state_action", stateAction.Name),
	)

	switch stateAction {
	case sandbox.StateActionPause:
		err := o.pauseSandbox(ctx, node, sbx, filesystemOnly)
		if err != nil {
			if dberrors.IsForeignKeyViolation(err) {
				killErr := o.killSandboxOnNode(ctx, node, sbx, sandbox.KillReasonBaseTemplateMissing)
				logger.L().Error(ctx, "Pause failed due to missing base template, killed sandbox as fallback",
					logger.WithSandboxID(sbx.SandboxID),
					zap.String("base_template_id", sbx.BaseTemplateID),
					zap.String("kill_reason", sandbox.KillReasonBaseTemplateMissing.String()),
					zap.NamedError("pause_error", err),
					zap.NamedError("kill_error", killErr),
				)

				return fmt.Errorf("failed to pause sandbox '%s': base template no longer exists: %w", sbx.SandboxID, err)
			}

			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sbx.SandboxID, err)
		}

		return nil
	case sandbox.StateActionKill:
		return o.killSandboxOnNode(ctx, node, sbx, reason)
	}

	return nil
}

func (o *Orchestrator) killOrphanSandbox(ctx context.Context, sbx sandbox.Sandbox) {
	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		logger.L().Error(ctx, "Node not found for orphan sandbox kill",
			logger.WithSandboxID(sbx.SandboxID),
			logger.WithNodeID(sbx.NodeID),
			zap.String("kill_reason", sandbox.KillReasonOrphaned.String()),
		)

		return
	}

	err := o.killSandboxOnNode(ctx, node, sbx, sandbox.KillReasonOrphaned)
	if err != nil {
		logger.L().Error(ctx, "Failed to kill orphan sandbox on node",
			zap.Error(err),
			logger.WithSandboxID(sbx.SandboxID),
			logger.WithNodeID(sbx.NodeID),
			zap.String("kill_reason", sandbox.KillReasonOrphaned.String()),
		)
	}
}

func (o *Orchestrator) killSandboxOnNode(
	ctx context.Context,
	node *nodemanager.Node,
	sbx sandbox.Sandbox,
	reason sandbox.KillReason,
) error {
	killReason := reason.String()
	req := &orchestrator.SandboxDeleteRequest{
		SandboxId:  sbx.SandboxID,
		KillReason: &killReason,
	}

	client, ctx := node.GetSandboxDeleteCtx(ctx, sbx.SandboxID, sbx.ExecutionID)
	_, err := client.Sandbox.Delete(ctx, req)
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.NotFound {
		logger.L().Info(ctx, "Sandbox not found during kill",
			logger.WithSandboxID(sbx.SandboxID),
			logger.WithNodeID(node.ID),
			zap.String("kill_reason", killReason),
		)
	} else if err != nil {
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}

	node.OptimisticRemove(ctx, nodemanager.SandboxResources{
		CPUs:      sbx.VCpu,
		MiBMemory: sbx.RamMB,
	})

	return nil
}
