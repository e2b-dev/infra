package orchestrator

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) DeleteInstance(ctx context.Context, sandbox *instance.InstanceInfo, pause bool) {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	zap.L().Debug("Delete instance from cache",
		logger.WithSandboxID(sandbox.SandboxID),
		zap.Bool("pause", pause),
	)

	o.instanceCache.Delete(sandbox, pause)
}
