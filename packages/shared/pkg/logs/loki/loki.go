package loki

import (
	"context"
	"fmt"
	"slices"

	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

func ResponseMapper(ctx context.Context, res *loghttp.QueryResponse, offset int32, level *logs.LogLevel, direction logproto.Direction) ([]logs.LogEntry, error) {
	logsCrawled := int32(0)
	logEntries := make([]logs.LogEntry, 0)

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		return nil, fmt.Errorf("unexpected value type received from loki query fetch: %s", res.Data.Result.Type())
	}

	for _, stream := range res.Data.Result.(loghttp.Streams) {
		for _, entry := range stream.Entries {
			fields, err := logs.FlatJsonLogLineParser(entry.Line)
			if err != nil {
				logger.L().Error(ctx, "error parsing log line", zap.Error(err), zap.String("line", entry.Line))
			}

			levelName := "info"
			if ll, ok := fields["level"]; ok {
				levelName = ll
			}

			// Skip logs that are below the specified level
			if level != nil && logs.CompareLevels(levelName, logs.LevelToString(*level)) < 0 {
				continue
			}

			// loki does not support offset pagination, so we need to skip logs manually
			logsCrawled++
			if logsCrawled <= offset {
				continue
			}

			message := ""
			if msg, ok := fields["message"]; ok {
				message = msg
			}

			// Drop duplicate fields
			delete(fields, "message")
			delete(fields, "level")

			logEntries = append(logEntries, logs.LogEntry{
				Timestamp: entry.Timestamp,
				Raw:       entry.Line,

				Level:   logs.StringToLevel(levelName),
				Message: message,
				Fields:  fields,
			})
		}
	}

	// Sort logs by timestamp (they are returned by the time they arrived in Loki)
	slices.SortFunc(logEntries, func(a, b logs.LogEntry) int {
		if direction == logproto.BACKWARD {
			return b.Timestamp.Compare(a.Timestamp)
		}

		return a.Timestamp.Compare(b.Timestamp)
	})

	return logEntries, nil
}
