package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDLogs(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDLogsParams) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		telemetry.WithTeamID(team.ID.String()),
	)

	clusterID := utils.WithClusterFallback(team.ClusterID)
	cluster, ok := a.clusters.GetClusterById(clusterID)
	if !ok {
		telemetry.ReportCriticalError(ctx, "error getting cluster by ID", fmt.Errorf("cluster with ID '%s' not found", clusterID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error getting cluster '%s'", clusterID))

		return
	}

	logs, apiErr := cluster.GetResources().GetSandboxLogs(ctx, team.ID.String(), sandboxID, params.Start, params.Limit)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, logs)
}
