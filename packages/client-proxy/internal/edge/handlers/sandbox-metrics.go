package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clickhouseutils "github.com/e2b-dev/infra/packages/clickhouse/pkg/utils"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1SandboxMetrics(c *gin.Context, sandboxID string, params api.V1SandboxMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandbox-metrics")
	defer span.End()

	start, end, err := clickhouseutils.GetSandboxStartEndTime(ctx, a.querySandboxMetricsProvider, params.TeamID, sandboxID, params.Start, params.End)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error when getting metrics time range: %s", err))

		return
	}

	start, end, err = clickhouseutils.ValidateRange(start, end)
	if err != nil {
		telemetry.ReportError(ctx, "error validating dates", err, telemetry.WithTeamID(params.TeamID))
		a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

		return
	}

	// Calculate the step size
	step := clickhouseutils.CalculateStep(start, end)

	metrics, err := a.querySandboxMetricsProvider.QuerySandboxMetrics(ctx, sandboxID, params.TeamID, start, end, step)
	if err != nil {
		logger.L().Error(ctx, "Error fetching sandbox metrics from ClickHouse",
			logger.WithSandboxID(sandboxID),
			logger.WithTeamID(params.TeamID),
			zap.Error(err),
		)

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error querying sandbox metrics: %s", err))

		return
	}

	apiMetrics := make([]api.SandboxMetric, len(metrics))
	for i, m := range metrics {
		apiMetrics[i] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.Timestamp.Unix(),
			CpuUsedPct:    float32(m.CPUUsedPercent),
			CpuCount:      int32(m.CPUCount),
			MemTotal:      int64(m.MemTotal),
			MemUsed:       int64(m.MemUsed),
			DiskTotal:     int64(m.DiskTotal),
			DiskUsed:      int64(m.DiskUsed),
		}
	}

	c.JSON(http.StatusOK, apiMetrics)
}
