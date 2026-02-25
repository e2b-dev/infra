package orchestrator

import (
	"context"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

// GetSandboxes returns instances for a given team.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error) {
	ctx, childSpan := tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.sandboxStore.TeamItems(ctx, teamID, states)
}

// HasRunningSandboxesForBaseTemplate reports whether any active sandbox
// (across all teams) uses the given template as its BaseTemplateID.
func (o *Orchestrator) HasRunningSandboxesForBaseTemplate(ctx context.Context, baseTemplateID string) (bool, error) {
	ctx, childSpan := tracer.Start(ctx, "has-running-sandboxes-for-base-template")
	defer childSpan.End()

	sandboxes, err := o.sandboxStore.AllItems(ctx, []sandbox.State{sandbox.StateRunning, sandbox.StatePausing, sandbox.StateSnapshotting, sandbox.StateKilling})
	if err != nil {
		return false, err
	}

	for _, sbx := range sandboxes {
		if sbx.BaseTemplateID == baseTemplateID {
			return true, nil
		}
	}

	return false, nil
}
