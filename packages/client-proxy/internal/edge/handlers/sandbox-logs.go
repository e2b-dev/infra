package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	sandboxLogsOldestLimit = 168 * time.Hour // 7 days
)

func (a *APIStore) V1SandboxLogs(c *gin.Context, sandboxID string, params api.V1SandboxLogsParams) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(c, "sandbox-logs-handler")
	defer templateSpan.End()

	end := time.Now()
	var start time.Time

	if params.Start != nil {
		start = time.UnixMilli(*params.Start)
	} else {
		start = end.Add(-sandboxLogsOldestLimit)
	}

	logsRaw, err := a.queryLogsProvider.QuerySandboxLogs(ctx, params.TeamID, sandboxID, start, end, int(*params.Limit))
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching sandbox logs")
		telemetry.ReportCriticalError(ctx, "error when fetching sandbox logs", err)
		return
	}

	l := make([]api.SandboxLog, 0, len(logsRaw))
	le := make([]api.SandboxLogEntry, 0, len(logsRaw))

	for _, log := range logsRaw {
		l = append(l, api.SandboxLog{Timestamp: log.Timestamp, Line: log.Raw})
		le = append(
			le, api.SandboxLogEntry{
				Timestamp: log.Timestamp,
				Message:   log.Message,
				Level:     api.LogLevel(logs.LevelToString(log.Level)),
				Fields:    log.Fields,
			},
		)
	}

	c.JSON(http.StatusOK, api.SandboxLogsResponse{Logs: l, LogEntries: le})
}
