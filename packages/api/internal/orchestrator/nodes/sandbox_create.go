package nodes

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (n *Node) SandboxCreate(ctx context.Context, sbxRequest *orchestrator.SandboxCreateRequest) error {
	client, ctx := n.GetClient(ctx)
	_, err := client.Sandbox.Create(n.GetSandboxCreateCtx(ctx, sbxRequest), sbxRequest)
	if err != nil {
		return err
	}

	return nil
}
