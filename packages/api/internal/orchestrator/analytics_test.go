package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func TestSandboxStopTime(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name      string
		startTime time.Time
		endTime   time.Time
		want      time.Time
	}{
		{
			// Normal kill/pause before timeout: StartRemoving already moved
			// EndTime to now for non-expired sandboxes; stop time is now.
			name:      "end time in the future",
			startTime: now.Add(-time.Hour),
			endTime:   now.Add(time.Hour),
			want:      now,
		},
		{
			// Late eviction of a stale record
			name:      "end time long past",
			startTime: now.Add(-30 * 24 * time.Hour),
			endTime:   now.Add(-30*24*time.Hour + time.Hour),
			want:      now.Add(-30*24*time.Hour + time.Hour),
		},
		{
			name:      "end time just passed",
			startTime: now.Add(-time.Hour),
			endTime:   now.Add(-time.Minute),
			want:      now.Add(-time.Minute),
		},
		{
			// Corrupt record: EndTime before StartTime must not produce a
			// negative duration; fall back to now.
			name:      "end time before start time",
			startTime: now.Add(-time.Hour),
			endTime:   now.Add(-2 * time.Hour),
			want:      now,
		},
		{
			name:      "zero end time",
			startTime: now.Add(-time.Hour),
			endTime:   time.Time{},
			want:      now,
		},
		{
			name:      "now before start time",
			startTime: now.Add(time.Second),
			endTime:   now.Add(time.Hour),
			want:      now.Add(time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sbx := sandbox.Sandbox{StartTime: tt.startTime, EndTime: tt.endTime}
			got := sbxStopTime(sbx, now)
			require.True(t, tt.want.Equal(got), "want %v, got %v", tt.want, got)
			require.GreaterOrEqual(t, got.Sub(tt.startTime), time.Duration(0), "sandbox duration must never be negative")
		})
	}
}
