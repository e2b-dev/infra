package logs

import "time"

type LogEntry struct {
	Timestamp time.Time
	Message   string
	Level     LogLevel
}

type LogLevel int32

const (
	LevelDebug LogLevel = 0
	LevelInfo  LogLevel = 1
	LevelWarn  LogLevel = 2
	LevelError LogLevel = 3
)

var stringToLevel = map[string]LogLevel{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

var levelToString = map[LogLevel]string{
	LevelDebug: "debug",
	LevelInfo:  "info",
	LevelWarn:  "warn",
	LevelError: "error",
}

func StringToLevel(name string) LogLevel {
	if level, ok := stringToLevel[name]; ok {
		return level
	}
	return LevelInfo // Default to info if not found
}

func LevelToString(level LogLevel) string {
	if name, ok := levelToString[level]; ok {
		return name
	}
	return "info"
}

func compareLevels(as, bs string) int32 {
	a := stringToLevel[as]
	b := stringToLevel[bs]

	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}
