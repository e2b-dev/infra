package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const getSandboxesMetricsTimeout = 30 * time.Second

func (a *APIStore) getSandboxesMetrics(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxes []utils.PaginatedSandbox,
) ([]api.RunningSandboxWithMetrics, error) {
	// Add operation telemetry
	telemetry.ReportEvent(ctx, "fetch metrics for sandboxes")
	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		attribute.Int("sandboxes.count", len(sandboxes)),
	)

	startTime := time.Now()
	defer func() {
		// Report operation duration
		duration := time.Since(startTime)
		telemetry.SetAttributes(ctx,
			attribute.Float64("operation.duration_ms", float64(duration.Milliseconds())),
		)
	}()

	sandboxIds := make([]string, 0, len(sandboxes))
	for _, s := range sandboxes {
		sandboxIds = append(sandboxIds, s.SandboxID)
	}

	flagCtx := ldcontext.NewBuilder(featureflags.MetricsReadFlagName).Build()
	metricsReadFlag, err := a.featureFlags.Ld.BoolVariation(featureflags.MetricsReadFlagName, flagCtx, featureflags.MetricsReadDefault)
	if err != nil {
		zap.L().Error("soft failing during metrics write feature flag receive", zap.Error(err))
	}

	// Get metrics for all sandboxes
	metrics, err := getSandboxesSandboxIDMetrics(
		ctx,
		a.clickhouseStore,
		metricsReadFlag,
		sandboxIds,
		teamID.String(),
	)
	if err != nil {
		zap.L().Error("Error fetching sandbox metrics from ClickHouse",
			logger.WithTeamID(teamID.String()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("error fetching sandbox metrics: %w", err)
	}

	// Collect results and build final response
	sandboxesWithMetrics := make([]api.RunningSandboxWithMetrics, 0, len(sandboxes))

	// Process each result as it arrives
	for _, sbx := range sandboxes {
		var sbxMetrics *[]api.SandboxMetric
		m, ok := metrics[sbx.SandboxID]
		if ok {
			// TODO: Does this need to be an array? and also pointer?
			sbxMetrics = &[]api.SandboxMetric{m}
		}

		sbxWithMetrics := api.RunningSandboxWithMetrics{
			Alias:      sbx.Alias,
			ClientID:   sbx.ClientID,
			CpuCount:   sbx.CpuCount,
			EndAt:      sbx.EndAt,
			MemoryMB:   sbx.MemoryMB,
			Metadata:   sbx.Metadata,
			Metrics:    sbxMetrics,
			SandboxID:  sbx.SandboxID,
			StartedAt:  sbx.StartedAt,
			TemplateID: sbx.TemplateID,
		}

		sandboxesWithMetrics = append(sandboxesWithMetrics, sbxWithMetrics)
	}

	return sandboxesWithMetrics, nil
}

func (a *APIStore) GetSandboxesMetrics(c *gin.Context, params api.GetSandboxesMetricsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list running instances with metrics")

	// Cancel context after timeout to ensure no goroutines are left hanging for too long
	ctx, cancel := context.WithTimeout(ctx, getSandboxesMetricsTimeout)
	defer cancel()

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed running instances with metrics", properties)

	metadataFilter, err := utils.ParseMetadata(params.Metadata)
	if err != nil {
		zap.L().Error("Error parsing metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error parsing metadata: %s", err))

		return
	}

	// Get relevant running sandboxes
	sandboxes := getRunningSandboxes(ctx, a.orchestrator, team.ID, metadataFilter)

	sandboxesWithMetrics, err := a.getSandboxesMetrics(ctx, team.ID, sandboxes)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error fetching metrics for sandboxes", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandboxes for team '%s'", team.ID))

		return
	}

	c.JSON(http.StatusOK, sandboxesWithMetrics)
}
