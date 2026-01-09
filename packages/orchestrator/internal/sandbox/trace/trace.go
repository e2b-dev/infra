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

	// FC operation types (for phase tracking)
	TypeFCConfigure    uint8 = 10
	TypeFCLoadSnapshot uint8 = 11
	TypeFCUffdReady    uint8 = 12
	TypeFCResume       uint8 = 13
	TypeFCMmds         uint8 = 14
	TypeNBDConnect     uint8 = 15
	TypeEnvdWait       uint8 = 16

	// Granular sub-phases
	TypeFCStart           uint8 = 20 // FC process start (cmd.Start)
	TypeFCSocketWait      uint8 = 21 // Wait for FC socket
	TypeUffdSocketWait    uint8 = 22 // Wait for UFFD socket
	TypeFCLoadSnapshotAPI uint8 = 23 // LoadSnapshot API call
	TypeUffdReadyWait     uint8 = 24 // Wait for UFFD ready signal
	TypeNBDWait           uint8 = 25 // Wait for NBD to be ready
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

// PhaseEvent represents a named phase with timing.
type PhaseEvent struct {
	Name      string `json:"name"`
	Timestamp int64  `json:"ts"`  // Unix nanoseconds
	Duration  int64  `json:"dur"` // Duration in nanoseconds
	Type      uint8  `json:"typ"`
}

// PhaseRecorder records named phase events for FC operations.
type PhaseRecorder struct {
	mu      sync.Mutex
	events  []PhaseEvent
	enabled bool
}

// NewPhaseRecorder creates a new phase recorder.
func NewPhaseRecorder(enabled bool) *PhaseRecorder {
	r := &PhaseRecorder{enabled: enabled}
	if enabled {
		r.events = make([]PhaseEvent, 0, 32)
	}
	return r
}

// SetEnabled enables or disables recording.
func (r *PhaseRecorder) SetEnabled(enabled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
	if enabled && r.events == nil {
		r.events = make([]PhaseEvent, 0, 32)
	}
}

// IsEnabled returns whether recording is enabled.
func (r *PhaseRecorder) IsEnabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// Record adds a phase event.
func (r *PhaseRecorder) Record(name string, startTime time.Time, eventType uint8) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	r.events = append(r.events, PhaseEvent{
		Name:      name,
		Timestamp: startTime.UnixNano(),
		Duration:  time.Since(startTime).Nanoseconds(),
		Type:      eventType,
	})
}

// Events returns a copy of all recorded phase events.
func (r *PhaseRecorder) Events() []PhaseEvent {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]PhaseEvent, len(r.events))
	copy(result, r.events)
	return result
}

// Clear removes all recorded events.
func (r *PhaseRecorder) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = r.events[:0]
}
