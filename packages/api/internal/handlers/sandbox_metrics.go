package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDMetrics(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandbox-metrics")
	defer span.End()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	// Build the context for feature flags
	ctx = featureflags.AddToContext(
		ctx,
		ldcontext.NewBuilder(sandboxID).
			Kind(featureflags.SandboxKind).
			Build(),
		ldcontext.NewBuilder(team.ID.String()).
			Kind(featureflags.TeamKind).
			Build(),
	)

	metricsReadFlag := a.featureFlags.BoolFlag(ctx, featureflags.MetricsReadFlag)
	if !metricsReadFlag {
		logger.L().Debug(ctx, "sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading

		c.JSON(http.StatusOK, []api.SandboxMetric{})

		return
	}

	clusterID := utils.WithClusterFallback(team.ClusterID)
	cluster, found := a.clusters.GetClusterById(clusterID)
	if !found {
		logger.L().Error(ctx, "cluster not found for sandbox metrics", logger.WithClusterID(clusterID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "cluster not found for sandbox metrics")

		return
	}

	metrics, apiErr := cluster.GetResources().GetSandboxMetrics(ctx, team.ID.String(), sandboxID, params.Start, params.End)
	if apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error getting sandbox metrics", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, metrics)
}
