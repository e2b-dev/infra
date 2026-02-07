package orchestrator

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (o *Orchestrator) RemoveSandbox(ctx context.Context, sbx sandbox.Sandbox, stateAction sandbox.StateAction) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox")
	defer span.End()

	sandboxID := sbx.SandboxID
	alreadyDone, finish, err := o.sandboxStore.StartRemoving(ctx, sbx.TeamID, sandboxID, stateAction)
	if err != nil {
		switch stateAction {
		case sandbox.StateActionKill:
			switch sbx.State {
			case sandbox.StateKilling:
				logger.L().Info(ctx, "Sandbox is already killed", logger.WithSandboxID(sandboxID))

				return nil
			default: // It shouldn't happen the sandbox ended in paused state
				logger.L().Error(ctx, "Error killing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

				return ErrSandboxOperationFailed
			}
		case sandbox.StateActionPause:
			switch sbx.State {
			case sandbox.StateKilling:
				logger.L().Info(ctx, "Sandbox is already killed", logger.WithSandboxID(sandboxID))

				return ErrSandboxNotFound
			default:
				logger.L().Error(ctx, "Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

				return ErrSandboxOperationFailed
			}
		default:
			logger.L().Error(ctx, "Invalid state action", logger.WithSandboxID(sandboxID), zap.String("state_action", string(stateAction)))

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

	defer func() { go o.countersRemove(context.WithoutCancel(ctx), sbx, stateAction) }()
	defer func() { go o.analyticsRemove(context.WithoutCancel(ctx), sbx, stateAction) }()
	defer o.sandboxStore.Remove(ctx, sbx.TeamID, sbx.SandboxID)
	err = o.removeSandboxFromNode(ctx, sbx, stateAction)
	if err != nil {
		logger.L().Error(ctx, "Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))

		return ErrSandboxOperationFailed
	}

	return nil
}

func (o *Orchestrator) removeSandboxFromNode(ctx context.Context, sbx sandbox.Sandbox, stateAction sandbox.StateAction) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox-from-node")
	defer span.End()

	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		logger.L().Error(ctx, "failed to get node", logger.WithNodeID(sbx.NodeID))

		return fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	// Only remove from routing table if the node is managed by Nomad
	// For remote cluster nodes we are using gPRC metadata for routing registration instead
	if node.IsNomadManaged() {
		// Remove the sandbox resources after the sandbox is deleted
		err := o.routingCatalog.DeleteSandbox(ctx, sbx.SandboxID, sbx.ExecutionID)
		if err != nil {
			logger.L().Error(ctx, "error removing routing record from catalog", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
		}
	}

	sbxlogger.I(sbx).Debug(ctx, "Removing sandbox",
		zap.Bool("auto_pause", sbx.AutoPause),
		zap.String("state_action", string(stateAction)),
	)

	switch stateAction {
	case sandbox.StateActionPause:
		err := o.pauseSandbox(ctx, node, sbx)
		if err != nil {
			logger.L().Debug(ctx, "failed to create snapshot", logger.WithSandboxID(sbx.SandboxID), zap.String("base_template_id", sbx.BaseTemplateID))

			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sbx.SandboxID, err)
		}

		return nil
	case sandbox.StateActionKill:
		req := &orchestrator.SandboxDeleteRequest{SandboxId: sbx.SandboxID}

		client, ctx := node.GetSandboxDeleteCtx(ctx, sbx.SandboxID, sbx.ExecutionID)
		_, err := client.Sandbox.Delete(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", sbx.SandboxID, err)
		}
	}

	return nil
}
