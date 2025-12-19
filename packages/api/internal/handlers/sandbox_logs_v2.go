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
	apiedge "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetV2SandboxesSandboxIDLogs(c *gin.Context, sandboxID string, params api.GetV2SandboxesSandboxIDLogsParams) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		telemetry.WithTeamID(team.ID.String()),
	)

	cluster, ok := a.clustersPool.GetClusterById(utils.WithClusterFallback(team.ClusterID))
	if !ok {
		telemetry.ReportCriticalError(ctx, "error getting cluster by ID", fmt.Errorf("cluster with ID '%s' not found", team.ClusterID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting cluster")

		return
	}

	// map api direction to edge direction
	var direction *apiedge.V2SandboxLogsParamsDirection
	if params.Direction != nil {
		d := apiedge.V2SandboxLogsParamsDirection(*params.Direction)
		direction = &d
	}

	res, err := cluster.GetHttpClient().V2SandboxLogsWithResponse(
		ctx, sandboxID, &apiedge.V2SandboxLogsParams{
			TeamID:    team.ID.String(),
			Start:     params.Cursor,
			Limit:     params.Limit,
			Direction: direction,
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

	logs := make([]api.SandboxLogEntry, 0, len(res.JSON200.Logs))
	for _, row := range res.JSON200.Logs {
		logs = append(logs, api.SandboxLogEntry{
			Timestamp: row.Timestamp,
			Level:     api.LogLevel(row.Level),
			Message:   row.Message,
			Fields:    row.Fields,
		})
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logs})
}
