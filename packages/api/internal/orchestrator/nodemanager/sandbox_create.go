package nodemanager

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandboxroutingcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func (n *Node) SandboxCreate(ctx context.Context, sbxRequest *orchestrator.SandboxCreateRequest, routingKeepalive *sandboxroutingcatalog.Keepalive) error {
	client, ctx := n.GetSandboxCreateCtx(ctx, sbxRequest, routingKeepalive)
	_, err := client.Sandbox.Create(ctx, sbxRequest)
	if err != nil {
		return err
	}

	return nil
}
