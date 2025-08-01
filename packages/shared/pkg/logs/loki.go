package logs

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/grafana/loki/pkg/loghttp"
	"go.uber.org/zap"
)

func LokiBasicResponseMapper(res *loghttp.QueryResponse, offset int32) ([]loghttp.Entry, error) {
	logsCrawled := int32(0)

	switch res.Data.Result.Type() {
	case loghttp.ResultTypeStream:
		value := res.Data.Result.(loghttp.Streams)

		logs := make([]loghttp.Entry, 0)
		for _, stream := range value {
			for _, entry := range stream.Entries {
				// loki does not support offset pagination, so we need to skip logs manually
				logsCrawled++
				if logsCrawled <= offset {
					continue
				}

				logs = append(logs, entry)
			}
		}

		// Sort logs by timestamp (they are returned by the time they arrived in Loki)
		slices.SortFunc(logs, func(a, b loghttp.Entry) int {
			return a.Timestamp.Compare(b.Timestamp)
		})

		return logs, nil
	default:
		return nil, fmt.Errorf("unexpected value type received from loki query fetch: %s", res.Data.Result.Type())
	}
}

// LokiResponseMapper maps the Loki query response to a slice of LogEntry that assumes log object structure
func LokiResponseMapper(res *loghttp.QueryResponse, offset int32, level *LogLevel) ([]LogEntry, error) {
	logsCrawled := int32(0)
	logs := make([]LogEntry, 0)

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		return nil, fmt.Errorf("unexpected value type received from loki query fetch: %s", res.Data.Result.Type())
	}

	for _, stream := range res.Data.Result.(loghttp.Streams) {
		for _, entry := range stream.Entries {
			line := make(map[string]interface{})
			err := json.Unmarshal([]byte(entry.Line), &line)
			if err != nil {
				zap.L().Error("error parsing log line", zap.Error(err), zap.String("line", entry.Line))
			}

			levelName := "info"
			if ll, ok := line["level"]; ok {
				levelName = ll.(string)
			}

			// Skip logs that are below the specified level
			if level != nil && compareLevels(levelName, LevelToString(*level)) < 0 {
				continue
			}

			// loki does not support offset pagination, so we need to skip logs manually
			logsCrawled++
			if logsCrawled <= offset {
				continue
			}

			logs = append(logs, LogEntry{
				Timestamp: entry.Timestamp,
				Message:   line["message"].(string),
				Level:     StringToLevel(levelName),
			})
		}
	}

	// Sort logs by timestamp (they are returned by the time they arrived in Loki)
	slices.SortFunc(logs, func(a, b LogEntry) int { return a.Timestamp.Compare(b.Timestamp) })

	return logs, nil
}
