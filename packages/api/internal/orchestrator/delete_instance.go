package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) DeleteInstanceRequest(ctx context.Context, sandboxID string, nodeID *string) error {
	if nodeID == nil {
		sbx, err := o.instanceCache.GetInstance(sandboxID)
		if err != nil {
			return fmt.Errorf("failed to get sandbox '%s': %w", sandboxID, err)
		}
		nodeID = &sbx.Instance.ClientID
	}

	node, err := o.GetNode(*nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node '%s': %w", nodeID, err)
	}

	_, err = node.Client.Sandbox.Delete(ctx, &orchestrator.SandboxRequest{
		SandboxID: sandboxID,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox '%s': %w", sandboxID, err)
	}

	return nil
}
