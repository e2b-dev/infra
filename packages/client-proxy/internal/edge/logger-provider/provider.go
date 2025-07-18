package logger_provider

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type LogsQueryProvider interface {
	QueryBuildLogs(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int, level *LogLevel) ([]LogEntry, error)
	QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error)
}

type LogLevel int32

const (
	LevelDebug LogLevel = 0
	LevelInfo  LogLevel = 1
	LevelWarn  LogLevel = 2
	LevelError LogLevel = 3
)

var levelNames = map[string]LogLevel{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

func APILevelToNumber(level *api.LogLevel) *LogLevel {
	if level == nil {
		return nil
	}

	l := levelNames[string(*level)]
	return &l
}

func LevelToAPILevel(level LogLevel) api.LogLevel {
	for name, num := range levelNames {
		if num == level {
			return api.LogLevel(name)
		}
	}

	return api.LogLevelInfo
}

type LogEntry struct {
	Timestamp time.Time
	Line      string
	Level     LogLevel
}

func GetLogsQueryProvider() (LogsQueryProvider, error) {
	return NewLokiQueryProvider()
}
