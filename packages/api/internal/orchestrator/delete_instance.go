package orchestrator

import (
	"context"
	
	"go.uber.org/zap"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string, pause bool) bool {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	zap.L().Debug("Delete instance from cache",
		zap.String("sandbox_id", sandboxID),
		zap.Bool("pause", pause),
	)

	return o.instanceCache.Delete(sandboxID, pause)
}
