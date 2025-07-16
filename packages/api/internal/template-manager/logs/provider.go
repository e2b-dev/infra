package logs

import (
	"context"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildID string, offset *int32, level *api.LogLevel) ([]api.BuildLogEntry, error)
}

type LogLevel int32

const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

var levelNames = map[string]LogLevel{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

func levelToNumber(level *api.LogLevel) LogLevel {
	if level == nil {
		return LevelInfo
	}

	return levelNames[string(*level)]
}

func numberToLevel(level LogLevel) api.LogLevel {
	for name, num := range levelNames {
		if num == level {
			return api.LogLevel(name)
		}
	}

	return api.LogLevelInfo
}
