package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) GetSandboxesSandboxIDMetrics(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := a.Tracer.Start(ctx, "sandbox-metrics")
	defer span.End()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	metricsReadFlag, err := a.featureFlags.BoolFlag(featureflags.MetricsReadFlagName, sandboxID)
	if err != nil {
		zap.L().Error("error getting metrics read feature flag, soft failing", zap.Error(err))
	}

	if !metricsReadFlag {
		zap.L().Debug("sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading

		c.JSON(http.StatusOK, []api.SandboxMetric{})
		return
	}

	start, end, err := getSandboxStartEndTime(ctx, a.clickhouseStore, team.ID.String(), sandboxID, params)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error when getting metrics time range: %s", err))
		return
	}

	// Validate time range parameters
	if start.After(end) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "start time cannot be after end time")
		return
	}

	step := calculateStep(start, end)

	metrics, err := a.clickhouseStore.QuerySandboxMetrics(ctx, sandboxID, team.ID.String(), start, end, step)
	if err != nil {
		zap.L().Error("Error fetching sandbox metrics from ClickHouse",
			logger.WithSandboxID(sandboxID),
			logger.WithTeamID(team.ID.String()),
			zap.Error(err),
		)

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error querying sandbox metrics: %s", err))
		return
	}

	apiMetrics := make([]api.SandboxMetric, len(metrics))
	for i, m := range metrics {
		apiMetrics[i] = api.SandboxMetric{
			Timestamp:  m.Timestamp,
			CpuUsedPct: float32(m.CPUUsedPercent),
			CpuCount:   int32(m.CPUCount),
			MemTotal:   int64(m.MemTotal),
			MemUsed:    int64(m.MemUsed),
			DiskTotal:  int64(m.DiskTotal),
			DiskUsed:   int64(m.DiskUsed),
		}
	}

	c.JSON(http.StatusOK, apiMetrics)
}

// calculateStep determines the step size for metrics based on the time range.
// The result should always contain less than 1000 points.
func calculateStep(start, end time.Time) time.Duration {
	// Calculate the step size in seconds
	duration := end.Sub(start)
	switch {
	case duration < time.Hour:
		return 5 * time.Second
	case duration < 6*time.Hour:
		return 30 * time.Second
	case duration < 12*time.Hour:
		return time.Minute
	default:
		return 2 * time.Minute
	}
}

func getSandboxStartEndTime(ctx context.Context, clickhouseStore clickhouse.Clickhouse, teamID, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) (time.Time, time.Time, error) {
	// Check if the sandbox exists
	var start, end time.Time
	if params.Start != nil {
		start = time.Unix(*params.Start, 0)
	}

	if params.End != nil {
		end = time.Unix(*params.End, 0)
	}

	if start.IsZero() || end.IsZero() {
		sbxStart, sbxEnd, err := clickhouseStore.QuerySandboxTimeRange(ctx, sandboxID, teamID)
		if err != nil {
			zap.L().Error("Error fetching sandbox time range from ClickHouse",
				logger.WithSandboxID(sandboxID),
				logger.WithTeamID(teamID),
				zap.Error(err),
			)

			return time.Time{}, time.Time{}, fmt.Errorf("error querying sandbox time range: %w", err)
		}

		if start.IsZero() {
			start = sbxStart
		}

		if end.IsZero() {
			end = sbxEnd
		}
	}
	return start, end, nil
}
