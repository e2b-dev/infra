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
	alreadyDone, finish, err := o.sandboxStore.StartRemoving(ctx, sandboxID, stateAction)
	if err != nil {
		switch stateAction {
		case sandbox.StateActionKill:
			switch sbx.State {
			case sandbox.StateKilling:
				zap.L().Info("Sandbox is already killed", logger.WithSandboxID(sandboxID))

				return nil
			default: // It shouldn't happen the sandbox ended in paused state
				zap.L().Error("Error killing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

				return ErrSandboxOperationFailed
			}
		case sandbox.StateActionPause:
			switch sbx.State {
			case sandbox.StateKilling:
				zap.L().Info("Sandbox is already killed", logger.WithSandboxID(sandboxID))

				return ErrSandboxNotFound
			default:
				zap.L().Error("Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

				return ErrSandboxOperationFailed
			}
		default:
			zap.L().Error("Invalid state action", logger.WithSandboxID(sandboxID), zap.String("state_action", string(stateAction)))

			return ErrSandboxOperationFailed
		}
	}
	defer func() {
		finish(err)
	}()

	if alreadyDone {
		zap.L().Info("Sandbox was already in the process of being removed", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State)))

		return nil
	}

	defer func() { go o.countersRemove(context.WithoutCancel(ctx), sbx, stateAction) }()
	defer func() { go o.analyticsRemove(context.WithoutCancel(ctx), sbx, stateAction) }()
	defer o.sandboxStore.Remove(ctx, sbx.TeamID.String(), sbx.SandboxID)
	err = o.removeSandboxFromNode(ctx, sbx, stateAction)
	if err != nil {
		zap.L().Error("Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))

		return ErrSandboxOperationFailed
	}

	return nil
}

func (o *Orchestrator) removeSandboxFromNode(ctx context.Context, sbx sandbox.Sandbox, stateAction sandbox.StateAction) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox-from-node")
	defer span.End()

	node := o.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		zap.L().Error("failed to get node", logger.WithNodeID(sbx.NodeID))

		return fmt.Errorf("node '%s' not found", sbx.NodeID)
	}

	// Remove the sandbox resources after the sandbox is deleted
	defer node.RemoveSandbox(sbx)

	err := o.routingCatalog.DeleteSandbox(ctx, sbx.SandboxID, sbx.ExecutionID)
	if err != nil {
		zap.L().Error("error removing routing record from catalog", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
	}

	sbxlogger.I(sbx).Debug("Removing sandbox",
		zap.Bool("auto_pause", sbx.AutoPause),
		zap.String("state_action", string(stateAction)),
	)

	switch stateAction {
	case sandbox.StateActionPause:
		var err error
		err = o.pauseSandbox(ctx, node, sbx)
		if err != nil {
			zap.L().Debug("failed to create snapshot", logger.WithSandboxID(sbx.SandboxID), zap.String("base_template_id", sbx.BaseTemplateID))

			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sbx.SandboxID, err)
		}
	case sandbox.StateActionKill:
		var err error
		req := &orchestrator.SandboxDeleteRequest{SandboxId: sbx.SandboxID}
		client, ctx := node.GetClient(ctx)
		_, err = client.Sandbox.Delete(node.GetSandboxDeleteCtx(ctx, sbx.SandboxID, sbx.ExecutionID), req)
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", sbx.SandboxID, err)
		}
	}

	return nil
}
