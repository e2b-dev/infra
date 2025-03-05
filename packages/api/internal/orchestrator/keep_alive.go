package orchestrator

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	sbx, apiErr := o.instanceCache.KeepAliveFor(sandboxID, duration, allowShorter)
	if apiErr != nil {
		return apiErr
	}

	err := o.UpdateSandbox(ctx, sbx.Instance.SandboxID, sbx.GetEndTime(), sbx.Instance.ClientID)
	if err != nil {
		zap.L().Error("Error when setting sandbox timeout", zap.Error(err), zap.String("sandbox_id", sandboxID))
		return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}
