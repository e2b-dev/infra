package orchestrator

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandboxID string, pause bool) bool {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	zap.L().Debug("Delete instance from cache",
		logger.WithSandboxID(sandboxID),
		zap.Bool("pause", pause),
	)

	c, err := o.instanceCache.Get(sandboxID)
	if err != nil && c != nil {
		node := o.GetNode(c.Node.ID)
		if node != nil {
			err := o.RemoveSandboxFromClusterCatalog(node, sandboxID, c.ExecutionID)
			if err != nil {
				zap.L().Error("Failed to remove sandbox from cluster catalog", logger.WithSandboxID(sandboxID), zap.Error(err))
			}
		}
	}

	return o.instanceCache.Delete(sandboxID, pause)
}
