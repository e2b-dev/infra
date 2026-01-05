package uffd

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	DiffMetadata(ctx context.Context) (*header.DiffMetadata, error)
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
	// SetTraceEnabled enables or disables page fault tracing.
	SetTraceEnabled(enabled bool)
	// GetPageFaultTrace returns page fault events (timestamp, offset, durations).
	GetPageFaultTrace() []PageFaultEvent
}

// PageFaultEvent represents a single page fault with timing information.
type PageFaultEvent struct {
	Timestamp int64 `json:"ts"`  // Unix nanoseconds when the fault started
	Duration  int64 `json:"dur"` // Total time to serve the fault (nanoseconds)
	Offset    int64 `json:"off"` // Offset in the memfile
}

// TraceEvent represents a named event with timing information.
type TraceEvent struct {
	Timestamp int64  `json:"ts"`   // Unix nanoseconds when event occurred
	Name      string `json:"name"` // Event name
}

// TraceRecorder collects trace events during sandbox operations.
type TraceRecorder struct {
	events  []TraceEvent
	enabled bool
}

// NewTraceRecorder creates a new trace recorder.
func NewTraceRecorder(enabled bool) *TraceRecorder {
	return &TraceRecorder{
		events:  make([]TraceEvent, 0, 32),
		enabled: enabled,
	}
}

// Record adds a trace event with the current timestamp.
func (r *TraceRecorder) Record(name string) {
	if r == nil || !r.enabled {
		return
	}
	r.events = append(r.events, TraceEvent{
		Timestamp: time.Now().UnixNano(),
		Name:      name,
	})
}

// Events returns all recorded events.
func (r *TraceRecorder) Events() []TraceEvent {
	if r == nil {
		return nil
	}
	return r.events
}
