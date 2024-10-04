package orchestrator

import (
	"context"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string) bool {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	return o.instanceCache.Kill(sandboxID)
}
