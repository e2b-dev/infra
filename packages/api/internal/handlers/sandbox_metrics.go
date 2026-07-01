package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDMetrics(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandbox-metrics")
	defer span.End()
	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c)

	clusterID := clusters.WithClusterFallback(team.ClusterID)
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
