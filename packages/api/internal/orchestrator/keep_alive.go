package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var errMaxInstanceLengthExceeded = fmt.Errorf("max instance length exceeded")

func (o *Orchestrator) KeepAliveFor(ctx context.Context, teamID uuid.UUID, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	now := time.Now()

	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, &sandbox.NotFoundError{SandboxID: sandboxID}
		}

		// Calculate the maximum TTL that can be set without exceeding the max instance length
		ttl := getMaxAllowedTTL(now, sbx.StartTime, duration, sbx.MaxInstanceLength)
		endTime := now.Add(ttl)

		if (time.Since(sbx.StartTime)) > sbx.MaxInstanceLength {
			return sbx, errMaxInstanceLengthExceeded
		}

		if !allowShorter && endTime.Before(sbx.EndTime) {
			return sbx, sandbox.ErrCannotShortenTTL
		}

		logger.L().Debug(ctx, "sandbox ttl updated", logger.WithSandboxID(sbx.SandboxID), zap.Time("end_time", endTime))
		sbx.EndTime = endTime

		return sbx, nil
	}

	var sbxNotFoundErr *sandbox.NotFoundError
	sbx, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		switch {
		case errors.As(err, &sbxNotFoundErr):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		case errors.Is(err, errMaxInstanceLengthExceeded):
			return &api.APIError{Code: http.StatusBadRequest, ClientMsg: "Max instance length exceeded", Err: err}
		case errors.Is(err, sandbox.ErrCannotShortenTTL):
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

// getMaxAllowedTTL calculates the maximum allowed TTL for a sandbox without exceeding its max instance length.
func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}
