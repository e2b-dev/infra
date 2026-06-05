package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// GetV2SandboxesSandboxIDCommandsCidLogs returns the persisted output (stdout/stderr)
// of a single command, scoped by the envd-assigned command ID (cid). It reuses the
// sandbox logs pipeline, adding the cid as a filter.
func (a *APIStore) GetV2SandboxesSandboxIDCommandsCidLogs(c *gin.Context, sandboxID api.SandboxID, cid string, params api.GetV2SandboxesSandboxIDCommandsCidLogsParams) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	if cid == "" {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid command ID")

		return
	}

	team := auth.MustGetTeamInfo(c)

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.String("command.id", cid),
		telemetry.WithTeamID(team.ID.String()),
	)

	direction := api.LogsDirectionBackward
	if params.Direction != nil {
		direction = *params.Direction
	}

	var cursor *time.Time
	if params.Cursor != nil {
		cursor = new(time.UnixMilli(*params.Cursor))
	}

	start, end := clusters.LogQueryWindow(cursor, direction)

	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	logs, apiErr := a.getSandboxLogs(ctx, team, sandboxID, &startMs, &endMs, params.Limit, &direction, apiToLogLevel(params.Level), params.Search, &cid)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error when returning logs for command", apiErr.Err)

		return
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logs.LogEntries})
}
