package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) KeepAliveFor(ctx context.Context, sandboxID string, duration time.Duration, allowShorter bool) *api.APIError {
	data, err := o.GetSandboxData(sandboxID, false)
	if err != nil {
		return &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("Sandbox '%s' is not running anymore", sandboxID), Err: err}
	}

	if (time.Since(data.StartTime)) > data.MaxInstanceLength {
		msg := fmt.Sprintf("Sandbox '%s' reached maximal allowed uptime", sandboxID)
		return &api.APIError{Code: http.StatusForbidden, ClientMsg: msg, Err: errors.New(msg)}
	}

	now := time.Now()
	maxAllowedTTL := getMaxAllowedTTL(now, data.StartTime, duration, data.MaxInstanceLength)
	newEndTime := now.Add(maxAllowedTTL)
	zap.L().Debug("sandbox ttl updated", logger.WithSandboxID(data.SandboxID), zap.Time("end_time", newEndTime))

	updated, err := o.sandboxStore.ExtendEndTime(sandboxID, newEndTime, allowShorter)
	if err != nil {
		return &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("Sandbox '%s' is not running anymore", sandboxID), Err: err}
	}

	if !updated {
		// No need to update in orchestrator
		return nil
	}

	err = o.UpdateSandbox(ctx, data.SandboxID, newEndTime, data.ClusterID, data.NodeID)
	if err != nil {
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
