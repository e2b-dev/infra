package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	getSandboxesMetricsTimeout = 30 * time.Second
	maxConcurrentMetricFetches = 30
)

func (a *APIStore) getSandboxesMetrics(
	ctx context.Context,
	teamID uuid.UUID,
	sandboxes []PaginatedSandbox,
) ([]api.RunningSandboxWithMetrics, error) {
	// Add operation telemetry
	telemetry.ReportEvent(ctx, "fetch metrics for sandboxes")
	telemetry.SetAttributes(ctx,
		attribute.String("team.id", teamID.String()),
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

	type metricsResult struct {
		sandbox PaginatedSandbox
		metrics []api.SandboxMetric
		err     error
	}

	results := make(chan metricsResult, len(sandboxes))
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(maxConcurrentMetricFetches))

	// Error tracking with atomic counters
	var errorCount atomic.Int32
	var timeoutCount atomic.Int32
	var successCount atomic.Int32
	var metricsErrors []error

	// Fetch metrics for each sandbox concurrently with rate limiting
	for _, sandbox := range sandboxes {
		wg.Add(1)
		go func(s PaginatedSandbox) {
			defer wg.Done()

			err := sem.Acquire(ctx, 1)
			if err != nil {
				timeoutCount.Add(1)
				err := fmt.Errorf("context cancelled while waiting for rate limiter: %w", ctx.Err())
				telemetry.ReportError(ctx, err)
				results <- metricsResult{
					sandbox: s,
					err:     err,
				}

				return
			}
			defer sem.Release(1)

			// Get metrics for this sandbox
			metrics, err := a.LegacyGetSandboxIDMetrics(
				ctx,
				s.SandboxID,
				teamID.String(),
				1,
				oldestLogsLimit,
			)

			if err != nil {
				errorCount.Add(1)
				telemetry.ReportError(ctx, fmt.Errorf("failed to fetch metrics for sandbox %s: %w", s.SandboxID, err))
			} else {
				successCount.Add(1)
			}

			results <- metricsResult{
				sandbox: s,
				metrics: metrics,
				err:     err,
			}
		}(sandbox)
	}

	// Close results channel once all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and build final response
	sandboxesWithMetrics := make([]api.RunningSandboxWithMetrics, 0, len(sandboxes))

	// Process each result as it arrives
	for result := range results {
		sandbox := api.RunningSandboxWithMetrics{
			ClientID:   result.sandbox.ClientID,
			TemplateID: result.sandbox.TemplateID,
			Alias:      result.sandbox.Alias,
			SandboxID:  result.sandbox.SandboxID,
			StartedAt:  result.sandbox.StartedAt,
			CpuCount:   result.sandbox.CpuCount,
			MemoryMB:   result.sandbox.MemoryMB,
			EndAt:      result.sandbox.EndAt,
			Metadata:   result.sandbox.Metadata,
		}

		if result.err == nil {
			sandbox.Metrics = &result.metrics
		} else {
			metricsErrors = append(metricsErrors, result.err)
		}

		sandboxesWithMetrics = append(sandboxesWithMetrics, sandbox)
	}

	// Report final metrics
	telemetry.SetAttributes(ctx,
		attribute.Int("metrics.success_count", int(successCount.Load())),
		attribute.Int("metrics.error_count", int(errorCount.Load())),
		attribute.Int("metrics.timeout_count", int(timeoutCount.Load())),
	)

	// Log operation summary
	if len(metricsErrors) == 0 {
		zap.L().Info("Completed fetching sandbox metrics without errors",
			zap.String("team_id", teamID.String()),
			zap.Int32("total_sandboxes", int32(len(sandboxes))),
			zap.Int32("successful_fetches", successCount.Load()),
			zap.Int32("timeouts", timeoutCount.Load()),
			zap.Duration("duration", time.Since(startTime)),
		)
	} else {
		errorStrings := make([]string, len(metricsErrors))
		for i, err := range metricsErrors {
			errorStrings[i] = err.Error()
		}

		err := errors.Join(metricsErrors...)

		zap.L().Error("Received errors while fetching metrics for some sandboxes",
			zap.String("team_id", teamID.String()),
			zap.Int32("total_sandboxes", int32(len(sandboxes))),
			zap.Int32("successful_fetches", successCount.Load()),
			zap.Int32("failed_fetches", errorCount.Load()),
			zap.Int32("timeouts", timeoutCount.Load()),
			zap.Duration("duration", time.Since(startTime)),
			zap.Error(err),
		)
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

	sandboxes, _, err := a.getSandboxes(ctx, team.ID, SandboxesListParams{
		State:    &[]api.SandboxState{api.Running},
		Metadata: params.Metadata,
	}, SandboxListPaginationParams{
		Limit:     nil,
		NextToken: nil,
	})

	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning sandboxes for team '%s': %s", team.ID, err))

		return
	}

	sandboxesWithMetrics, err := a.getSandboxesMetrics(ctx, team.ID, sandboxes)
	if err != nil {
		zap.L().Error("Error fetching metrics for sandboxes", zap.Error(err))
		telemetry.ReportCriticalError(ctx, err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandboxes for team '%s'", team.ID))

		return
	}

	c.JSON(http.StatusOK, sandboxesWithMetrics)
}
