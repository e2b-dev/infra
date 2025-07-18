package cache

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type ZapEntry struct {
	Ts    float64 `json:"ts"`
	Msg   string  `json:"msg"`
	Level string  `json:"level"`
}

type SafeBuffer struct {
	mu    sync.Mutex
	lines []*template_manager.TemplateBuildLogEntry
}

func NewSafeBuffer() *SafeBuffer {
	return &SafeBuffer{
		lines: make([]*template_manager.TemplateBuildLogEntry, 0),
	}
}

func (b *SafeBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for line := range bytes.SplitSeq(p, []byte("\n")) {
		if len(line) > 0 {
			var entry ZapEntry
			err := json.Unmarshal(line, &entry)
			if err != nil {
				zap.L().Error("failed to unmarshal log entry", zap.Error(err), zap.ByteString("line", line))
				continue
			}

			timestamp := epochToTime(entry.Ts)
			b.lines = append(b.lines, &template_manager.TemplateBuildLogEntry{
				Timestamp: timestamppb.New(timestamp),
				Message:   entry.Msg,
				Level:     stringToLogLevel(entry.Level),
			})
		}
	}
	return len(p), nil
}

func (b *SafeBuffer) Sync() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// No-op for SafeBuffer, as it doesn't have an underlying file to sync
	// But wait for the mutex to ensure no writes are happening
	return nil
}

func stringToLogLevel(level string) template_manager.LogLevel {
	switch level {
	case "debug":
		return template_manager.LogLevel_Debug
	case "info":
		return template_manager.LogLevel_Info
	case "warn":
		return template_manager.LogLevel_Warn
	case "error":
		return template_manager.LogLevel_Error
	default:
		return template_manager.LogLevel_Info
	}
}

func (b *SafeBuffer) Lines() []*template_manager.TemplateBuildLogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Shallow copy of the slice (not the entries themselves)
	copied := make([]*template_manager.TemplateBuildLogEntry, len(b.lines))
	copy(copied, b.lines)
	return copied
}

func epochToTime(epoch float64) time.Time {
	// split into integer seconds and fractional part
	sec := int64(epoch)
	nsec := int64((epoch - float64(sec)) * 1e9) // convert fractional part to nanoseconds

	// convert to time.Time
	return time.Unix(sec, nsec).UTC()
}
