package orchestrator

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (o *Orchestrator) RemoveInstance(ctx context.Context, sandbox *instance.InstanceInfo, removeType instance.RemoveType) error {
	_, childSpan := tracer.Start(ctx, "remove-instance")
	defer childSpan.End()

	// SandboxStore will remove the sandbox both from the store and from the orchestrator
	return o.sandboxStore.Remove(ctx, sandbox.SandboxID, removeType)
}

// removeSandbox should be called from places where you already marked the sandbox as being removed
func (o *Orchestrator) removeSandbox(ctx context.Context, sandbox *instance.InstanceInfo, removeType instance.RemoveType) error {
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
		zap.String("remove_type", string(removeType)),
	)

	switch removeType {
	case instance.RemoveTypePause:
		var err error
		err = o.pauseSandbox(ctx, node, sandbox)
		if err != nil {
			return fmt.Errorf("failed to auto pause sandbox '%s': %w", sandbox.SandboxID, err)
		}
	case instance.RemoveTypeKill:
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
