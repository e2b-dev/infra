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
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const maxSandboxMetricsCount = 100

func (a *APIStore) getSandboxesMetrics(
	ctx context.Context,
	teamID uuid.UUID,
	clusterID uuid.UUID,
	sandboxIDs []string,
) (map[string]api.SandboxMetric, *api.APIError) {
	ctx, span := tracer.Start(ctx, "fetch-sandboxes-metrics")
	defer span.End()

	for i, id := range sandboxIDs {
		sandboxIDs[i] = utils.ShortID(id)
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		attribute.Int("sandboxes.count", len(sandboxIDs)),
	)

	// Get metrics for all sandboxes
	metricsReadFlag := a.featureFlags.BoolFlag(ctx, featureflags.MetricsReadFlag)
	if !metricsReadFlag {
		logger.L().Debug(ctx, "sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading
		return make(map[string]api.SandboxMetric), nil
	}

	cluster, found := a.clusters.GetClusterById(clusterID)
	if !found {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "cluster not found for sandbox metrics",
			Err:       fmt.Errorf("cluster not found for sandbox metrics, cluster id: %s", clusterID.String()),
		}
	}

	metrics, apiErr := cluster.GetResources().GetSandboxesMetrics(ctx, teamID.String(), sandboxIDs)
	if apiErr != nil {
		return nil, apiErr
	}

	return metrics, nil
}

func (a *APIStore) GetSandboxesMetrics(c *gin.Context, params api.GetSandboxesMetricsParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list running instances with metrics")

	team := c.Value(auth.TeamContextKey).(*types.Team)

	if len(params.SandboxIds) > maxSandboxMetricsCount {
		logger.L().Error(ctx, "Too many sandboxes requested", zap.Int("requested_count", len(params.SandboxIds)), zap.Int("max_count", maxSandboxMetricsCount), logger.WithTeamID(team.ID.String()))
		telemetry.ReportError(ctx, "too many sandboxes requested", fmt.Errorf("requested %d, max %d", len(params.SandboxIds), maxSandboxMetricsCount), telemetry.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Too many sandboxes requested, maximum is %d", maxSandboxMetricsCount))

		return
	}

	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "listed running instances with metrics", properties)

	// Build the context for feature flags
	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(team.ID.String()).
			Kind(featureflags.TeamKind).
			Build(),
	)

	sandboxesWithMetrics, apiErr := a.getSandboxesMetrics(ctx, team.ID, utils.WithClusterFallback(team.ClusterID), params.SandboxIds)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error fetching sandboxes metrics", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, &api.SandboxesWithMetrics{Sandboxes: sandboxesWithMetrics})
}
