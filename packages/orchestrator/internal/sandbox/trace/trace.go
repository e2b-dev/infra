// Package trace provides shared tracing infrastructure for sandbox operations.
package trace

import (
	"sync"
	"time"
)

// Event represents a single traced event with timing information.
type Event struct {
	Timestamp int64 `json:"ts"`  // Unix nanoseconds when the event started
	Duration  int64 `json:"dur"` // Duration in nanoseconds (0 for point events)
	Offset    int64 `json:"off"` // Offset/position (context-dependent)
	Length    int64 `json:"len"` // Length/size (context-dependent)
	Type      uint8 `json:"typ"` // Event type (context-dependent)
}

// Event types for categorization
const (
	TypeRead  uint8 = 0
	TypeWrite uint8 = 1
	TypeFault uint8 = 2
)

// EventRecorder is a thread-safe recorder for trace events.
type EventRecorder struct {
	mu      sync.Mutex
	events  []Event
	enabled bool
}

// NewEventRecorder creates a new event recorder.
// If enabled is false, recording operations are no-ops.
func NewEventRecorder(enabled bool) *EventRecorder {
	r := &EventRecorder{
		enabled: enabled,
	}
	if enabled {
		r.events = make([]Event, 0, 1024)
	}

	return r
}

// SetEnabled enables or disables recording.
func (r *EventRecorder) SetEnabled(enabled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
	if enabled && r.events == nil {
		r.events = make([]Event, 0, 1024)
	}
}

// IsEnabled returns whether recording is enabled.
func (r *EventRecorder) IsEnabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.enabled
}

// Record adds an event if recording is enabled.
// startTime is when the event started, offset/length are context-dependent.
func (r *EventRecorder) Record(startTime time.Time, offset, length int64, eventType uint8) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.events = append(r.events, Event{
		Timestamp: startTime.UnixNano(),
		Duration:  time.Since(startTime).Nanoseconds(),
		Offset:    offset,
		Length:    length,
		Type:      eventType,
	})
}

// RecordNow adds a point event (no duration) at the current time.
func (r *EventRecorder) RecordNow(offset, length int64, eventType uint8) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.events = append(r.events, Event{
		Timestamp: time.Now().UnixNano(),
		Duration:  0,
		Offset:    offset,
		Length:    length,
		Type:      eventType,
	})
}

// Events returns a copy of all recorded events.
func (r *EventRecorder) Events() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Event, len(r.events))
	copy(result, r.events)

	return result
}

// Clear removes all recorded events.
func (r *EventRecorder) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = r.events[:0]
}

// Count returns the number of recorded events.
func (r *EventRecorder) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.events)
}
