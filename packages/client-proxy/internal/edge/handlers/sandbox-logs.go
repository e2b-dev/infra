package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grafana/loki/pkg/logproto"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	sandboxLogsOldestLimit   = 168 * time.Hour // 7 days
	sandboxLogsDefaultRange  = 24 * time.Hour  // 1 day
	defaultLogsLimit         = 1000
	defaultLogsLimitV2       = 1000
	defaultSandboxLogsV2Dir  = logproto.FORWARD
)

func (a *APIStore) V1SandboxLogs(c *gin.Context, sandboxID string, params api.V1SandboxLogsParams) {
	ctx := c.Request.Context()

	_, templateSpan := tracer.Start(c, "sandbox-logs-handler")
	defer templateSpan.End()

	end := time.Now()
	var start time.Time

	if params.Start != nil {
		start = time.UnixMilli(*params.Start)
	} else {
		start = end.Add(-sandboxLogsOldestLimit)
	}

	limit := defaultLogsLimit
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	logsRaw, err := a.queryLogsProvider.QuerySandboxLogs(ctx, params.TeamID, sandboxID, start, end, limit)
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

func (a *APIStore) V2SandboxLogs(c *gin.Context, sandboxID string, params api.V2SandboxLogsParams) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "sandbox-logs-v2-handler")
	defer span.End()

	direction := defaultSandboxLogsV2Dir
	if params.Direction != nil && *params.Direction == api.V2SandboxLogsParamsDirectionBackward {
		direction = logproto.BACKWARD
	}

	start, end := time.Now().Add(-sandboxLogsDefaultRange), time.Now()
	if params.Start != nil {
		start = time.UnixMilli(*params.Start)
	}
	if params.End != nil {
		end = time.UnixMilli(*params.End)
	}

	limit := defaultLogsLimitV2
	if params.Limit != nil {
		limit = int(*params.Limit)
	}

	logsRaw, err := a.queryLogsProvider.QuerySandboxLogsV2(ctx, params.TeamID, sandboxID, start, end, limit, direction)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching sandbox logs")
		telemetry.ReportCriticalError(ctx, "error when fetching sandbox logs", err)

		return
	}

	logEntries := make([]api.SandboxLogEntry, 0, len(logsRaw))
	for _, log := range logsRaw {
		logEntries = append(logEntries, api.SandboxLogEntry{
			Timestamp: log.Timestamp,
			Message:   log.Message,
			Level:     api.LogLevel(logs.LevelToString(log.Level)),
			Fields:    log.Fields,
		})
	}

	c.JSON(http.StatusOK, api.SandboxLogsV2Response{Logs: logEntries})
}
