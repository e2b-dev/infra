package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func TestSbxStopTime(t *testing.T) {
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
			// Clock skew: record starts in the future.
			name:      "start time in the future",
			startTime: now.Add(time.Hour),
			endTime:   now.Add(2 * time.Hour),
			want:      now.Add(time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sbx := sandbox.Sandbox{StartTime: tt.startTime, EndTime: tt.endTime}
			got := sbxStopTime(sbx, now)
			require.True(t, tt.want.Equal(got), "want %v, got %v", tt.want, got)
			require.GreaterOrEqual(t, got.Sub(tt.startTime), time.Duration(0), "duration must never be negative")
		})
	}
}

func TestSbxStopTime_CorruptEndTime(t *testing.T) {
	t.Parallel()

	now := time.Now()
	start := now.Add(-30 * 24 * time.Hour)

	tests := []struct {
		name    string
		endTime time.Time
	}{
		{
			name:    "end time before start time",
			endTime: start.Add(-55 * time.Second),
		},
		{
			name:    "end time equal to start time",
			endTime: start,
		},
		{
			name:    "zero end time",
			endTime: time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sbx := sandbox.Sandbox{StartTime: start, EndTime: tt.endTime}
			got := sbxStopTime(sbx, now)

			// Corrupt record must be zero: stop time collapses to StartTime.
			require.True(t, start.Equal(got), "want StartTime %v, got %v", start, got)
			require.Zero(t, got.Sub(sbx.StartTime), "duration must be zero for corrupt records")
		})
	}
}
