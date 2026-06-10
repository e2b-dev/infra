package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// GetSandboxesSandboxIDLogsCommandPid returns the persisted output (stdout/stderr)
// of a single command, scoped by its process ID (pid) and a time window. It reuses
// the sandbox logs pipeline, adding the pid as a filter. The window disambiguates a
// reused pid: within [start, end] a pid maps to a single command execution. When
// start/end are omitted, the resources layer falls back to the full retention window.
func (a *APIStore) GetSandboxesSandboxIDLogsCommandPid(c *gin.Context, sandboxID api.SandboxID, pid int32, params api.GetSandboxesSandboxIDLogsCommandPidParams) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	if pid < 0 {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid command pid")

		return
	}

	team := auth.MustGetTeamInfo(c)

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.Int("command.pid", int(pid)),
		telemetry.WithTeamID(team.ID.String()),
	)

	// Command output is returned chronologically within the window.
	direction := api.LogsDirectionForward
	pidStr := strconv.Itoa(int(pid))

	logs, apiErr := a.getSandboxLogs(ctx, team, sandboxID, params.Start, params.End, params.Limit, &direction, nil, nil, &pidStr)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error when returning logs for command", apiErr.Err)

		return
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logs.LogEntries})
}
