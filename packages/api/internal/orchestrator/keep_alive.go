package orchestrator

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	sbx, apiErr := o.instanceCache.KeepAliveFor(sandboxID, duration, allowShorter)
	if apiErr != nil {
		return apiErr
	}

	endTime := sbx.GetEndTime()

	err := o.UpdateSandbox(ctx, sbx.SandboxID, &endTime, nil, sbx.ClusterID, sbx.NodeID)
	if err != nil {
		zap.L().Warn("Error when setting sandbox timeout", zap.Error(err), logger.WithSandboxID(sandboxID))
		return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}
