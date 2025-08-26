package orchestrator

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandbox *instance.InstanceInfo, duration time.Duration, allowShorter bool) *api.APIError {
	sbx, apiErr := o.instanceCache.KeepAliveFor(sandbox, duration, allowShorter)
	if apiErr != nil {
		return apiErr
	}

	err := o.UpdateSandbox(ctx, sbx.SandboxID, sbx.EndTime, sbx.ClusterID, sbx.NodeID)
	if err != nil {
		zap.L().Warn("Error when setting sandbox timeout", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
		return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}
