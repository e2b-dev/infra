package nodemanager

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type SandboxCreateOptions struct {
	TrafficKeepalive bool
}

func (n *Node) SandboxCreate(ctx context.Context, sbxRequest *orchestrator.SandboxCreateRequest, opts SandboxCreateOptions) error {
	client, ctx := n.GetSandboxCreateCtx(ctx, sbxRequest, opts)
	_, err := client.Sandbox.Create(ctx, sbxRequest)
	if err != nil {
		return err
	}

	return nil
}
