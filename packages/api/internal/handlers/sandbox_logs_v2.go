package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	apiedge "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	sandboxLogsMaxTimeRange = 7 * 24 * time.Hour
)

func (a *APIStore) GetV2SandboxesSandboxIDLogs(c *gin.Context, sandboxID string, params api.GetV2SandboxesSandboxIDLogsParams) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := a.GetTeamInfo(c)

	telemetry.SetAttributes(ctx,
		telemetry.WithSandboxID(sandboxID),
		telemetry.WithTeamID(team.ID.String()),
	)

	cluster, ok := a.clustersPool.GetClusterById(utils.WithClusterFallback(team.ClusterID))
	if !ok {
		telemetry.ReportCriticalError(ctx, "error getting cluster by ID", fmt.Errorf("cluster with ID '%s' not found", team.ClusterID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting cluster")

		return
	}

	direction := api.LogsDirectionBackward
	if params.Direction != nil {
		direction = *params.Direction
	}

	start, end := time.Now().Add(-sandboxLogsMaxTimeRange), time.Now()
	if params.Cursor != nil {
		cursor := time.UnixMilli(*params.Cursor)
		if direction == api.LogsDirectionForward {
			start = cursor
			end = cursor.Add(sandboxLogsMaxTimeRange)
		} else {
			end = cursor
			start = cursor.Add(-sandboxLogsMaxTimeRange)
		}
	}

	var edgeDirection *apiedge.V1SandboxLogsParamsDirection
	if direction == api.LogsDirectionForward {
		edgeDirection = sharedUtils.ToPtr(apiedge.V1SandboxLogsParamsDirectionForward)
	} else {
		edgeDirection = sharedUtils.ToPtr(apiedge.V1SandboxLogsParamsDirectionBackward)
	}

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	res, err := cluster.GetHttpClient().V1SandboxLogsWithResponse(
		ctx, sandboxID, &apiedge.V1SandboxLogsParams{
			TeamID:    team.ID.String(),
			Start:     &startMs,
			End:       &endMs,
			Limit:     params.Limit,
			Direction: edgeDirection,
		},
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning logs for sandbox '%s'", sandboxID))

		return
	}

	if res.JSON200 == nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", fmt.Errorf("unexpected response for sandbox '%s': %s", sandboxID, string(res.Body)))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning logs for sandbox '%s'", sandboxID))

		return
	}

	logs := make([]api.SandboxLogEntry, 0, len(res.JSON200.LogEntries))
	for _, row := range res.JSON200.LogEntries {
		logs = append(logs, api.SandboxLogEntry{
			Timestamp: row.Timestamp,
			Level:     api.LogLevel(row.Level),
			Message:   row.Message,
			Fields:    row.Fields,
		})
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logs})
}
