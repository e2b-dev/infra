package logs

import (
	"encoding/json"
	"strconv"
	"time"
)

type LogEntry struct {
	Timestamp time.Time
	Message   string
	Raw       string
	Level     LogLevel
	Fields    map[string]string
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

func CompareLevels(as, bs string) int32 {
	a := stringToLevel[as]
	b := stringToLevel[bs]

	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

// FlatJsonLogLineParser parses a flat JSON log line into a map of string keys and values.
// Handles based on the documentation at https://pkg.go.dev/encoding/json#Unmarshal
func FlatJsonLogLineParser(input string) (map[string]string, error) {
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
			result[key] = strconv.FormatFloat(t, 'G', -1, 64)
		case bool:
			result[key] = strconv.FormatBool(t)
		default:
			// Reject arrays, objects, nulls, etc.
		}
	}

	return result, nil
}
