package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	errMaxInstanceLengthExceeded = fmt.Errorf("max instance length exceeded")
	errCannotSetTTL              = fmt.Errorf("cannot set ttl")
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	now := time.Now()

	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotFoundError{SandboxID: sandboxID}
		}

		maxAllowedTTL := getMaxAllowedTTL(now, sbx.StartTime, duration, sbx.MaxInstanceLength)
		newEndTime := now.Add(maxAllowedTTL)

		if (time.Since(sbx.StartTime)) > sbx.MaxInstanceLength {
			return sbx, errMaxInstanceLengthExceeded
		}

		if !allowShorter && newEndTime.Before(sbx.EndTime) {
			return sbx, errCannotSetTTL
		}

		zap.L().Debug("sandbox ttl updated", logger.WithSandboxID(sbx.SandboxID), zap.Time("end_time", newEndTime))
		sbx.EndTime = newEndTime

		return sbx, nil
	}

	var sbxNotFoundErr *sandbox.NotFoundError
	sbx, err := o.sandboxStore.Update(ctx, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotFoundErr):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		case errors.Is(err, errMaxInstanceLengthExceeded):
			return &api.APIError{Code: http.StatusBadRequest, ClientMsg: "Max instance length exceeded", Err: err}
		case errors.Is(err, errCannotSetTTL):
			// If shorter than the current end time, we don't extend, so we can return
			return nil
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
		}
	}
	err = o.UpdateSandbox(ctx, sandboxID, sbx.EndTime, sbx.ClusterID, sbx.NodeID)
	if err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		}

		return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when setting sandbox timeout", Err: err}
	}

	return nil
}

func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}
