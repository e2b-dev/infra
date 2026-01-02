package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	templateBuildLogsLimit        = 100
	templateBuildLogsDefaultRange = 24 * time.Hour // 1 day

	defaultDirection = logproto.FORWARD
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
	ctx, templateSpan := tracer.Start(ctx, "template-build-logs-handler")
	defer templateSpan.End()

	offset := int32(0)
	if params.Offset != nil {
		offset = *params.Offset
	}

	direction := defaultDirection
	if params.Direction != nil && *params.Direction == api.V1TemplateBuildLogsParamsDirectionBackward {
		direction = logproto.BACKWARD
	}

	start, end := time.Now().Add(-templateBuildLogsDefaultRange), time.Now()
	if params.Start != nil {
		start = time.UnixMilli(*params.Start)
	}
	if params.End != nil {
		end = time.UnixMilli(*params.End)
	}

	limit := templateBuildLogsLimit
	if params.Limit != nil && *params.Limit < templateBuildLogsLimit {
		limit = int(*params.Limit)
	}

	logsRaw, err := a.queryLogsProvider.QueryBuildLogs(ctx, params.TemplateID, buildID, start, end, limit, offset, apiLevelToLogLevel(params.Level), direction)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when fetching template build logs")
		telemetry.ReportCriticalError(ctx, "error when fetching template build logs", err)

		return
	}

	logger.L().Debug(ctx, "fetched template build logs",
		zap.Int("count", len(logsRaw)),
		logger.WithBuildID(buildID),
		logger.WithTemplateID(params.TemplateID),
		zap.Int32("req_offset", offset),
		zap.Time("req_start", start),
		zap.Time("req_end", end),
		zap.Int("req_limit", templateBuildLogsLimit),
		zap.String("req_level", utils.Sprintp(params.Level)),
	)

	logEntries := make([]api.BuildLogEntry, 0, len(logsRaw))
	for _, log := range logsRaw {
		logEntries = append(logEntries, api.BuildLogEntry{
			Timestamp: log.Timestamp,
			Message:   log.Message,
			Level:     api.LogLevel(logs.LevelToString(log.Level)),
			Fields:    log.Fields,
		})
	}

	c.JSON(
		http.StatusOK,
		api.TemplateBuildLogsResponse{
			LogEntries: logEntries,
		},
	)
}
