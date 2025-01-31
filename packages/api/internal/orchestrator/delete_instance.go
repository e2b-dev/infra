package orchestrator

import (
	"context"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string, pause bool) bool {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	info, err := o.instanceCache.GetInstance(sandboxID)
	if err != nil {
		return false
	}

	info.AutoPause = pause
	return o.instanceCache.Delete(sandboxID)
}
