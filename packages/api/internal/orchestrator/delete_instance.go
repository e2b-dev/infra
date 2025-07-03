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
			go o.RemoveSandboxFromClusterCatalog(node, sandboxID, c.ExecutionID)
		}
	}

	return o.instanceCache.Delete(sandboxID, pause)
}
