package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string) error {
	childCtx, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	instance, err := o.instanceCache.GetInstance(sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get instance '%s': %w", sandboxID, err)
	}

	nodeID := instance.Instance.ClientID
	client, err := o.GetClient(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", nodeID, err)
	}

	_, err = client.Sandbox.Delete(childCtx, &orchestrator.SandboxRequest{
		SandboxID: sandboxID,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox '%s': %w", sandboxID, err)
	}

	return nil
}
