package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	sbx, err := o.sandboxStore.KeepAliveFor(ctx, sandboxID, duration, allowShorter)
	if err != nil {
		if errors.Is(err, store.ErrSandboxNotFound) {
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		} else if errors.Is(err, store.ErrMaxSandboxUptimeReached) {
			return &api.APIError{Code: http.StatusBadRequest, ClientMsg: "Sandbox has reached maximum allowed uptime", Err: err}
		} else {
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
		}
	}

	err = o.UpdateSandbox(ctx, sbx.SandboxID, sbx.EndTime, sbx.ClusterID, sbx.NodeID)
	if err != nil {
		zap.L().Warn("Error when setting sandbox timeout", zap.Error(err), logger.WithSandboxID(sandboxID))
		return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}
