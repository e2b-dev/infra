package orchestrator

import "context"

func (o *Orchestrator) DeletePausedSandbox(ctx context.Context, sandboxID string) error {
	if o.pausedCatalog == nil {
		return nil
	}

	return o.pausedCatalog.DeletePaused(ctx, sandboxID)
}
