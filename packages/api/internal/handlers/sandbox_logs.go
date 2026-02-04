package handlers

import (
	"context"
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

	logs, apiErr := a.getSandboxLogs(ctx, team, sandboxID, params.Start, nil, params.Limit, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", apiErr.Err)

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

	direction := api.LogsDirectionBackward
	if params.Direction != nil {
		direction = *params.Direction
	}

	start, end := time.Now().Add(-clusters.SandboxLogsOldestLimit), time.Now()
	if params.Cursor != nil {
		cursor := time.UnixMilli(*params.Cursor)
		if direction == api.LogsDirectionForward {
			start = cursor
			end = cursor.Add(clusters.SandboxLogsOldestLimit)
		} else {
			end = cursor
			start = cursor.Add(-clusters.SandboxLogsOldestLimit)
		}
	}

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	logs, apiErr := a.getSandboxLogs(ctx, team, sandboxID, &startMs, &endMs, params.Limit, &direction)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", apiErr.Err)

		return
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logs.LogEntries})
}

func (a *APIStore) getSandboxLogs(
	ctx context.Context,
	team *types.Team,
	sandboxID string,
	start *int64,
	end *int64,
	limit *int32,
	direction *api.LogsDirection,
) (api.SandboxLogs, *api.APIError) {
	clusterID := utils.WithClusterFallback(team.ClusterID)
	cluster, ok := a.clusters.GetClusterById(clusterID)
	if !ok {
		return api.SandboxLogs{}, &api.APIError{
			Err:       fmt.Errorf("cluster with ID '%s' not found", clusterID),
			ClientMsg: "Failed to get cluster",
			Code:      http.StatusInternalServerError,
		}
	}

	logs, apiErr := cluster.GetResources().GetSandboxLogs(ctx, team.ID.String(), sandboxID, start, end, limit, direction)
	if apiErr != nil {
		return api.SandboxLogs{}, apiErr
	}

	return logs, nil
}
