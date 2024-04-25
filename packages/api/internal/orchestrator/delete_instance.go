package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, info *instance.InstanceInfo) error {
	nodeID := info.Instance.ClientID
	sandboxID := info.Instance.SandboxID

	if node, ok := o.nodes[nodeID]; ok {
		node.CPUUsage -= info.VCPU
		node.RamUsage -= info.RamMB
	}

	client, err := o.GetClientByNodeID(nodeID)
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
