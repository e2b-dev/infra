package orchestrator

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (o *Orchestrator) RemoveSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction instance.StateAction) error {
	_, childSpan := tracer.Start(ctx, "remove-sandbox-public")
	defer childSpan.End()

	sbx, err := o.GetSandbox(sandboxID, true)
	if err != nil {
		return ErrSandboxNotFound
	}

	if sbx.TeamID() != teamID {
		return ErrAccessForbidden
	}

	return o.removeSandbox(ctx, sbx, stateAction)
}

func (o *Orchestrator) removeSandbox(ctx context.Context, sbx *instance.InstanceInfo, stateAction instance.StateAction) error {
	_, childSpan := tracer.Start(ctx, "remove-sandbox")
	defer childSpan.End()

	sandboxID := sbx.SandboxID()
	done, finish, err := sbx.StartRemoving(ctx, stateAction)
	if err != nil {
		data := sbx.Data()
		if stateAction == instance.StateActionKill {
			switch data.State {
			case instance.StateFailed:
				zap.L().Error("Sandbox is in failed state", logger.WithSandboxID(sandboxID), zap.Error(data.Reason))
				return ErrSandboxOperationFailed
			case instance.StateKilled:
				zap.L().Info("Sandbox is already killed", logger.WithSandboxID(sandboxID))
				return nil
			default: // It shouldn't happen the sandbox ended in paused state
				zap.L().Error("Error killing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))
				return ErrSandboxOperationFailed
			}
		} else {
			switch data.State {
			case instance.StateFailed:
				zap.L().Error("Sandbox is in failed state", logger.WithSandboxID(sandboxID), zap.Error(data.Reason))
				return ErrSandboxOperationFailed
			case instance.StateKilled:
				zap.L().Info("Sandbox is already killed", logger.WithSandboxID(sandboxID))
				return ErrSandboxNotFound
			default:
				zap.L().Error("Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))
				return ErrSandboxOperationFailed
			}
		}
	}
	defer func() {
		finish(err)
	}()

	if done {
		zap.L().Info("Sandbox was already in the process of being removed", logger.WithSandboxID(sandboxID), zap.String("state", string(sbx.State())))

		return nil
	}

	sbx.SetExpired()
	data := sbx.Data()

	defer o.countersRemove(context.WithoutCancel(ctx), data, stateAction)
	defer o.analyticsRemove(context.WithoutCancel(ctx), data, stateAction)
	defer o.sandboxStore.Remove(sbx.SandboxID())
	err = o.removeSandboxFromNode(ctx, data, stateAction)
	if err != nil {
		zap.L().Error("Error pausing sandbox", zap.Error(err), logger.WithSandboxID(sbx.SandboxID()))
		return ErrSandboxOperationFailed
	}

	return nil
}

func (o *Orchestrator) removeSandboxFromNode(ctx context.Context, sandbox instance.Data, stateAction instance.StateAction) error {
	ctx, span := tracer.Start(ctx, "remove-sandbox-from-node")
	defer span.End()

	node := o.GetNode(sandbox.ClusterID, sandbox.NodeID)
	if node == nil {
		zap.L().Error("failed to get node", logger.WithNodeID(sandbox.NodeID))
		return fmt.Errorf("node '%s' not found", sandbox.NodeID)
	}

	// Remove the sandbox resources after the sandbox is deleted
	defer node.RemoveSandbox(sandbox)

	o.dns.Remove(ctx, sandbox.SandboxID, node.IPAddress)

	sbxlogger.I(sandbox).Debug("Removing sandbox",
		zap.Bool("auto_pause", sandbox.AutoPause),
		zap.String("state_action", string(stateAction)),
	)

	switch stateAction {
	case instance.StateActionPause:
		var err error
		err = o.pauseSandbox(ctx, node, sandbox)
		if err != nil {
			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sandbox.SandboxID, err)
		}
	case instance.StateActionKill:
		var err error
		req := &orchestrator.SandboxDeleteRequest{SandboxId: sandbox.SandboxID}
		client, ctx := node.GetClient(ctx)
		_, err = client.Sandbox.Delete(node.GetSandboxDeleteCtx(ctx, sandbox.SandboxID, sandbox.ExecutionID), req)
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", sandbox.SandboxID, err)
		}
	}

	return nil
}
