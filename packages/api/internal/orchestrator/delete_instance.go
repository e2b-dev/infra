package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, nodeID, sandboxID string) error {
	host, err := o.GetHost(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get host: %w", err)
	}

	client, err := o.GetClient(host)
	if err != nil {
		return fmt.Errorf("failed to get GRPC client: %w", err)
	}

	_, err = client.Sandbox.Delete(ctx, &orchestrator.SandboxRequest{
		SandboxID: sandboxID,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox '%s': %w", sandboxID, err)
	}

	return nil
}
