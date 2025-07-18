package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	loggerprovider "github.com/e2b-dev/infra/packages/proxy/internal/edge/logger-provider"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

func (a *APIStore) V1TemplateBuildLogs(c *gin.Context, buildID string, params api.V1TemplateBuildLogsParams) {
	ctx := c.Request.Context()

	_, templateSpan := a.tracer.Start(c, "template-build-logs-handler")
	defer templateSpan.End()

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)

	offset := 0
	if params.Offset != nil {
		offset = int(*params.Offset)
	}

	logsRaw, err := a.queryLogsProvider.QueryBuildLogs(ctx, params.TemplateID, buildID, start, end, templateBuildLogsLimit, offset, loggerprovider.APILevelToNumber(params.Level))
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching template build logs")
		telemetry.ReportCriticalError(ctx, "error when fetching template build logs", err)
		return
	}

	logs := make([]string, 0, len(logsRaw))
	logEntries := make([]api.BuildLogEntry, 0, len(logsRaw))
	for _, log := range logsRaw {
		logs = append(logs, log.Line)
		logEntries = append(logEntries, api.BuildLogEntry{
			Timestamp: log.Timestamp,
			Message:   log.Line,
			Level:     loggerprovider.LevelToAPILevel(log.Level),
		})
	}

	c.JSON(
		http.StatusOK,
		api.TemplateBuildLogsResponse{
			Logs:       logs,
			LogEntries: logEntries,
		},
	)
}
