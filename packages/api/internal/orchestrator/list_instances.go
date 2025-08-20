package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

func (o *Orchestrator) getSandboxes(ctx context.Context, clusterID uuid.UUID, nodeID string) ([]*instance.InstanceInfo, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, fmt.Errorf("node '%s' not found in cluster '%s'", nodeID, clusterID)
	}

	return n.GetSandboxes(ctx, o.tracer)
}

// GetSandboxes returns all instances for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID *uuid.UUID) []*instance.InstanceInfo {
	_, childSpan := o.tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.instanceCache.GetInstances(teamID)
}

func (o *Orchestrator) GetInstance(_ context.Context, id string) (*instance.InstanceInfo, error) {
	return o.instanceCache.Get(id)
}
