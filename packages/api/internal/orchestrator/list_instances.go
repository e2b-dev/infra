package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

func (o *Orchestrator) getSandboxes(ctx context.Context, clusterID uuid.UUID, nodeID string) ([]*store.Sandbox, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, fmt.Errorf("node '%s' not found in cluster '%s'", nodeID, clusterID)
	}

	return n.GetSandboxes(ctx, o.tracer)
}

// GetSandboxes returns all sandboxes for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID *uuid.UUID, states []store.State) map[store.State][]*store.Sandbox {
	_, childSpan := o.tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.sandboxStore.ItemsByState(ctx, teamID, states)
}
