package nodemanager

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (n *Node) SandboxCreate(ctx context.Context, sbxRequest *orchestrator.SandboxCreateRequest) (*orchestrator.SandboxCreateResponse, error) {
	client, ctx := n.GetSandboxCreateCtx(ctx, sbxRequest)

	return client.Sandbox.Create(ctx, sbxRequest)
}
