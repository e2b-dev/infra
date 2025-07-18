package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

type LokiProvider struct {
	LokiClient *client.DefaultClient
}

func (l *LokiProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset *int32, level *api.LogLevel) ([]api.BuildLogEntry, error) {
	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateID, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildID, templateIdSanitized)

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)
	logs := make([]api.BuildLogEntry, 0)

	res, err := l.LokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err == nil {
		logsCrawled := 0
		logsOffset := 0
		if offset != nil {
			logsOffset = int(*offset)
		}

		if res.Data.Result.Type() != loghttp.ResultTypeStream {
			zap.L().Error("unexpected value type received from loki query fetch", zap.String("type", string(res.Data.Result.Type())))
			return nil, fmt.Errorf("unexpected value type received from loki query fetch")
		}

		for _, stream := range res.Data.Result.(loghttp.Streams) {
			for _, entry := range stream.Entries {
				line := make(map[string]any)
				err := json.Unmarshal([]byte(entry.Line), &line)
				if err != nil {
					zap.L().Error("error parsing log line", zap.Error(err), logger.WithBuildID(buildID), zap.String("line", entry.Line))
				}

				entryLvl := api.LogLevelInfo
				if l, ok := line["level"]; ok {
					levelName := l.(string)
					level, ok := levelNames[levelName]
					if ok {
						entryLvl = numberToLevel(level)
					}
				}

				// Skip logs that are below the specified level
				if level != nil && levelToNumber(&entryLvl) < levelToNumber(level) {
					continue
				}

				// loki does not support offset pagination, so we need to skip logs manually
				logsCrawled++
				if logsCrawled <= logsOffset {
					continue
				}

				logs = append(logs, api.BuildLogEntry{
					Timestamp: entry.Timestamp,
					Message:   line["message"].(string),
					Level:     entryLvl,
				})
			}
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
	}

	return logs, nil
}
