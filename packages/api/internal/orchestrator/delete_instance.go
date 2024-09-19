package orchestrator

import (
	"context"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string) error {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	o.instanceCache.Kill(sandboxID)

	return nil
}
