package logs

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/grafana/loki/pkg/loghttp"
	"go.uber.org/zap"
)

func LokiResponseMapper(res *loghttp.QueryResponse, offset int32, level *LogLevel) ([]LogEntry, error) {
	logsCrawled := int32(0)
	logs := make([]LogEntry, 0)

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		return nil, fmt.Errorf("unexpected value type received from loki query fetch: %s", res.Data.Result.Type())
	}

	for _, stream := range res.Data.Result.(loghttp.Streams) {
		for _, entry := range stream.Entries {
			fields, err := lokiFlatJsonLineParser(entry.Line)
			if err != nil {
				zap.L().Error("error parsing log line", zap.Error(err), zap.String("line", entry.Line))
			}

			levelName := "info"
			if ll, ok := fields["level"]; ok {
				levelName = ll
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

			message := ""
			if msg, ok := fields["message"]; ok {
				message = msg
			}

			// Drop duplicate fields
			delete(fields, "message")
			delete(fields, "level")

			logs = append(logs, LogEntry{
				Timestamp: entry.Timestamp,
				Raw:       entry.Line,

				Level:   StringToLevel(levelName),
				Message: message,
				Fields:  fields,
			})
		}
	}

	// Sort logs by timestamp (they are returned by the time they arrived in Loki)
	slices.SortFunc(logs, func(a, b LogEntry) int { return a.Timestamp.Compare(b.Timestamp) })

	return logs, nil
}

func lokiFlatJsonLineParser(input string) (map[string]string, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for key, value := range raw {
		switch t := value.(type) {
		case string:
			result[key] = t
		case float64:
			result[key] = strconv.FormatFloat(value.(float64), 'E', -1, 64)
		case bool:
			result[key] = strconv.FormatBool(value.(bool))
		default:
			// Reject arrays, objects, nulls, etc.
		}
	}

	return result, nil
}
