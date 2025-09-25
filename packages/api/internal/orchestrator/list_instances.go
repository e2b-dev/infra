package orchestrator

import (
	"context"
	_ "embed"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

// GetSandboxes returns all instances for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID *uuid.UUID, states []instance.State) map[instance.State][]instance.Sandbox {
	_, childSpan := tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.sandboxStore.ItemsByState(teamID, states)
}
