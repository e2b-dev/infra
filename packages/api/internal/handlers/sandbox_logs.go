package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
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

	logs, apiErr := cluster.GetResources().GetSandboxLogs(ctx, team.ID.String(), sandboxID, params.Start, nil, params.Limit, nil)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, logs)
}

func (a *APIStore) GetV2SandboxesSandboxIDLogs(c *gin.Context, sandboxID api.SandboxID, params api.GetV2SandboxesSandboxIDLogsParams) {
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

	// Default to forward direction if not specified
	direction := api.LogsDirectionForward
	if params.Direction != nil {
		direction = *params.Direction
	}

	start, end := time.Now().Add(-clusters.MaxTimeRangeDuration), time.Now()
	if params.Cursor != nil {
		cursor := time.UnixMilli(*params.Cursor)
		if direction == api.LogsDirectionForward {
			start = cursor
			end = cursor.Add(clusters.MaxTimeRangeDuration)
		} else {
			end = cursor
			start = cursor.Add(-clusters.MaxTimeRangeDuration)
		}
	}

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	logs, apiErr := cluster.GetResources().GetSandboxLogs(ctx, team.ID.String(), sandboxID, &startMs, &endMs, params.Limit, params.Direction)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{
		Logs: logs.LogEntries,
	})
}
