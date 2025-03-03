package orchestrator

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	sbx, apiErr := o.instanceCache.KeepAliveFor(sandboxID, duration, allowShorter)
	if apiErr != nil {
		return apiErr
	}

	err := o.UpdateSandbox(ctx, sbx.Instance.SandboxID, sbx.GetEndTime(), sbx.Instance.ClientID)
	if err != nil {
		return &api.APIError{Code: 500, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}
