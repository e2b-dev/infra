package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	clickhouseUtils "github.com/e2b-dev/infra/packages/clickhouse/pkg/utils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) GetSandboxesSandboxIDMetrics(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandbox-metrics")
	defer span.End()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	// Build the context for feature flags
	ctx = featureflags.SetContext(
		ctx,
		ldcontext.NewBuilder(sandboxID).
			Kind(featureflags.SandboxKind).
			Build(),
		ldcontext.NewBuilder(team.ID.String()).
			Kind(featureflags.TeamKind).
			Build(),
	)

	metricsReadFlag := a.featureFlags.BoolFlag(ctx, featureflags.MetricsReadFlagName)

	if !metricsReadFlag {
		logger.L().Debug(ctx, "sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading

		c.JSON(http.StatusOK, []api.SandboxMetric{})

		return
	}

	// TODO: Remove in [ENG-3377], once edge is migrated
	edgeProvidedMetrics := a.featureFlags.BoolFlag(ctx, featureflags.EdgeProvidedSandboxMetricsFlagName)

	var metrics []api.SandboxMetric
	var apiErr *api.APIError
	if edgeProvidedMetrics {
		metrics, apiErr = clusters.GetClusterSandboxMetrics(
			ctx,
			a.clustersPool,
			sandboxID,
			team.ID.String(),
			utils.WithClusterFallback(team.ClusterID),
			params.Start,
			params.End,
		)
	} else {
		metrics, apiErr = a.getApiProvidedMetrics(ctx, team, sandboxID, params)
	}
	if apiErr != nil {
		logger.L().Error(ctx, "error getting sandbox metrics", zap.Error(apiErr.Err))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, metrics)
}

// TODO: Remove in [ENG-3377], once edge is migrated
func (a *APIStore) getApiProvidedMetrics(ctx context.Context, team *types.Team, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) ([]api.SandboxMetric, *api.APIError) {
	start, end, err := getSandboxStartEndTime(ctx, a.clickhouseStore, team.ID.String(), sandboxID, params)
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "error getting metrics time range",
			Err:       fmt.Errorf("error getting metrics time range: %w", err),
		}
	}

	start, end, err = clickhouseUtils.ValidateRange(start, end)
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: fmt.Sprintf("error validating time range: %s", err),
			Err:       err,
		}
	}

	// Calculate the step size
	step := clickhouseUtils.CalculateStep(start, end)

	metrics, err := a.clickhouseStore.QuerySandboxMetrics(ctx, sandboxID, team.ID.String(), start, end, step)
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "error querying sandbox metrics",
			Err:       fmt.Errorf("error querying sandbox metrics: %w", err),
		}
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

	return apiMetrics, nil
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
			logger.L().Error(ctx, "Error fetching sandbox time range from ClickHouse",
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
