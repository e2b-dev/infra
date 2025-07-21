package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

func apiLevelToLogLevel(level *api.LogLevel) *logs.LogLevel {
	if level == nil {
		return nil
	}

	value := logs.StringToLevel(string(*level))
	return &value
}

func (a *APIStore) V1TemplateBuildLogs(c *gin.Context, buildID string, params api.V1TemplateBuildLogsParams) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(c, "template-build-logs-handler")
	defer templateSpan.End()

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)

	offset := int32(0)
	if params.Offset != nil {
		offset = *params.Offset
	}

	logsRaw, err := a.queryLogsProvider.QueryBuildLogs(ctx, params.TemplateID, buildID, start, end, templateBuildLogsLimit, offset, apiLevelToLogLevel(params.Level))
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching template build logs")
		telemetry.ReportCriticalError(ctx, "error when fetching template build logs", err)
		return
	}

	lgs := make([]string, 0, len(logsRaw))
	logEntries := make([]api.BuildLogEntry, 0, len(logsRaw))
	for _, log := range logsRaw {
		lgs = append(lgs, log.Message)
		logEntries = append(logEntries, api.BuildLogEntry{
			Timestamp: log.Timestamp,
			Message:   log.Message,
			Level:     api.LogLevel(logs.LevelToString(log.Level)),
		})
	}

	c.JSON(
		http.StatusOK,
		api.TemplateBuildLogsResponse{
			Logs:       lgs,
			LogEntries: logEntries,
		},
	)
}
