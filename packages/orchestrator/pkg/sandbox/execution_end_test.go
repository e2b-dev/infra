//go:build linux

package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetStopReasonFirstCallWins(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	m.SetStopReason(StopReasonPaused)
	m.SetStopReason(StopReasonKilled)

	// A Delete landing on an already-pausing sandbox must not relabel it.
	assert.Equal(t, StopReasonPaused, m.GetStopReason())
}

func TestGetStopReasonDefaultsToCrashed(t *testing.T) {
	t.Parallel()

	m := &Metadata{}

	assert.Equal(t, StopReasonCrashed, m.GetStopReason())
}

func TestSetStoppedAtFirstCallWins(t *testing.T) {
	t.Parallel()

	suspended := time.Now()

	m := &Metadata{}
	m.SetStartedAt(suspended.Add(-time.Hour))
	m.SetStoppedAt(suspended)
	// The teardown after a pause lands once the snapshot and upload are done.
	m.SetStoppedAt(suspended.Add(30 * time.Second))

	duration, ok := m.ExecutionDuration()
	require.True(t, ok)
	assert.Equal(t, time.Hour, duration)
}

func TestExecutionDuration(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name      string
		startedAt time.Time
		stoppedAt time.Time
		want      time.Duration
		wantOK    bool
	}{
		{
			name:      "started and stopped",
			startedAt: now.Add(-time.Minute),
			stoppedAt: now,
			want:      time.Minute,
			wantOK:    true,
		},
		{
			// A failed envd init records no start, and a zero time would put a
			// two-millennia outlier in the histogram.
			name:      "never started",
			stoppedAt: now,
		},
		{
			name:      "still running",
			startedAt: now.Add(-time.Minute),
		},
		{
			name:      "stopped before it started",
			startedAt: now,
			stoppedAt: now.Add(-time.Minute),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Metadata{}
			if !tt.startedAt.IsZero() {
				m.SetStartedAt(tt.startedAt)
			}
			if !tt.stoppedAt.IsZero() {
				m.SetStoppedAt(tt.stoppedAt)
			}

			duration, ok := m.ExecutionDuration()
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, duration)
		})
	}
}
