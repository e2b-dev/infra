package orchestrator

import "context"

func (o *Orchestrator) WaitForStop(ctx context.Context, sandboxID string) error {
	return o.sandboxStore.WaitForStop(ctx, sandboxID)
}
