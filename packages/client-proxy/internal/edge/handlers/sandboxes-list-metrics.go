package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const maxSandboxMetricsCount = 100

func (a *APIStore) V1SandboxesMetrics(c *gin.Context, params api.V1SandboxesMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandboxes-metrics")
	defer span.End()

	if len(params.SandboxIds) > maxSandboxMetricsCount {
		logger.L().Error(ctx, "Too many sandboxes requested", zap.Int("requested_count", len(params.SandboxIds)), zap.Int("max_count", maxSandboxMetricsCount), logger.WithTeamID(params.TeamID))
		telemetry.ReportError(ctx, "too many sandboxes requested", fmt.Errorf("requested %d, max %d", len(params.SandboxIds), maxSandboxMetricsCount), telemetry.WithTeamID(params.TeamID))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Too many sandboxes requested, maximum is %d", maxSandboxMetricsCount))

		return
	}

	sandboxesWithMetrics, err := a.getSandboxesMetrics(ctx, params.TeamID, params.SandboxIds)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error fetching metrics for sandboxes", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandboxes for team '%s'", params.TeamID))

		return
	}

	c.JSON(http.StatusOK, api.SandboxesWithMetrics{Sandboxes: sandboxesWithMetrics})
}

func (a *APIStore) getSandboxesMetrics(
	ctx context.Context,
	teamID string,
	sandboxIDs []string,
) (map[string]api.SandboxMetric, error) {
	ctx, span := tracer.Start(ctx, "fetch-sandboxes-metrics")
	defer span.End()

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID),
		attribute.Int("sandboxes.count", len(sandboxIDs)),
	)

	metrics, err := a.querySandboxMetricsProvider.QueryLatestMetrics(ctx, sandboxIDs, teamID)
	if err != nil {
		logger.L().Error(ctx, "Error fetching sandbox metrics from ClickHouse",
			logger.WithTeamID(teamID),
			zap.Error(err),
		)

		return nil, fmt.Errorf("error querying metrics: %w", err)
	}

	apiMetrics := make(map[string]api.SandboxMetric)
	for _, m := range metrics {
		apiMetrics[m.SandboxID] = api.SandboxMetric{
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

	return apiMetrics, nil
}
