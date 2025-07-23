package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const maxSandboxMetricsCount = 100

func (a *APIStore) getSandboxesMetrics(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxIDs []string,
) (map[string]api.SandboxMetric, error) {
	ctx, span := a.Tracer.Start(ctx, "fetch-sandboxes-metrics")
	defer span.End()

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		attribute.Int("sandboxes.count", len(sandboxIDs)),
	)

	flagCtx := ldcontext.NewBuilder(featureflags.MetricsReadFlagName).Build()
	metricsReadFlag, err := a.featureFlags.Ld.BoolVariation(featureflags.MetricsReadFlagName, flagCtx, featureflags.MetricsReadDefault)
	if err != nil {
		zap.L().Error("error getting metrics read feature flag, soft failing", zap.Error(err))
	}

	// Get metrics for all sandboxes
	if !metricsReadFlag {
		zap.L().Debug("sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading
		return make(map[string]api.SandboxMetric), nil
	}

	metrics, err := a.clickhouseStore.QueryLatestMetrics(ctx, sandboxIDs, teamID.String())
	if err != nil {
		zap.L().Error("Error fetching sandbox metrics from ClickHouse",
			logger.WithTeamID(teamID.String()),
			zap.Error(err),
		)

		return nil, fmt.Errorf("error querying metrics: %w", err)
	}

	apiMetrics := make(map[string]api.SandboxMetric)
	for _, m := range metrics {
		apiMetrics[m.SandboxID] = api.SandboxMetric{
			Timestamp:  m.Timestamp,
			CpuUsedPct: float32(m.CPUUsedPercent),
			CpuCount:   int32(m.CPUCount),
			MemTotal:   int64(m.MemTotal),
			MemUsed:    int64(m.MemUsed),
			DiskTotal:  int64(m.DiskTotal),
			DiskUsed:   int64(m.DiskUsed),
		}
	}

	return apiMetrics, nil
}

func (a *APIStore) GetSandboxesMetrics(c *gin.Context, params api.GetSandboxesMetricsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list running instances with metrics")

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	if len(params.SandboxIds) > maxSandboxMetricsCount {
		zap.L().Error("Too many sandboxes requested", zap.Int("requested_count", len(params.SandboxIds)), zap.Int("max_count", maxSandboxMetricsCount), logger.WithTeamID(team.ID.String()))
		telemetry.ReportError(ctx, "too many sandboxes requested", fmt.Errorf("requested %d, max %d", len(params.SandboxIds), maxSandboxMetricsCount), telemetry.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Too many sandboxes requested, maximum is %d", maxSandboxMetricsCount))

		return
	}

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed running instances with metrics", properties)

	sandboxesWithMetrics, err := a.getSandboxesMetrics(ctx, team.ID, params.SandboxIds)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error fetching metrics for sandboxes", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandboxes for team '%s'", team.ID))

		return
	}

	c.JSON(http.StatusOK, &api.SandboxesWithMetrics{Sandboxes: sandboxesWithMetrics})
}
