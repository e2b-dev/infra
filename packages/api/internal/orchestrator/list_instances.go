package orchestrator

import (
	"context"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// GetSandboxes returns all instances for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID uuid.UUID, states []sandbox.State, options ...sandbox.ItemsOption) []sandbox.Sandbox {
	_, childSpan := tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.sandboxStore.Items(&teamID, states, options...)
}
