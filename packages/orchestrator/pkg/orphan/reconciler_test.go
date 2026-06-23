//go:build linux

package orphan_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/orphan"
)

// ─── nextSweepTime ───────────────────────────────────────────────────────────

func TestNextSweepTime_BeforeTargetTime_SameDay(t *testing.T) {
	t.Parallel()

	// 01:00 local → next 17:30 is still today
	now := time.Date(2026, 5, 28, 1, 0, 0, 0, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	want := time.Date(2026, 5, 28, 17, 30, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

func TestNextSweepTime_AfterTargetTime_NextDay(t *testing.T) {
	t.Parallel()

	// 18:00 local → today's 17:30 has passed, next is tomorrow
	now := time.Date(2026, 5, 28, 18, 0, 0, 0, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	want := time.Date(2026, 5, 29, 17, 30, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

func TestNextSweepTime_ExactlyAtTargetTime_NextDay(t *testing.T) {
	t.Parallel()

	// Exactly 17:30 → candidate equals now, not strictly after, so advance to tomorrow
	now := time.Date(2026, 5, 28, 17, 30, 0, 0, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	want := time.Date(2026, 5, 29, 17, 30, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

func TestNextSweepTime_OneSecondBefore_SameDay(t *testing.T) {
	t.Parallel()

	// 17:29:59 → next 17:30 is still today
	now := time.Date(2026, 5, 28, 17, 29, 59, 0, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	want := time.Date(2026, 5, 28, 17, 30, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

func TestNextSweepTime_OneSecondAfter_NextDay(t *testing.T) {
	t.Parallel()

	// 17:30:01 → just past the target, advance to tomorrow
	now := time.Date(2026, 5, 28, 17, 30, 1, 0, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	want := time.Date(2026, 5, 29, 17, 30, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

func TestNextSweepTime_AlwaysInFuture(t *testing.T) {
	t.Parallel()

	// For any "now", the returned time must be strictly after now.
	cases := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		time.Date(2026, 5, 28, 17, 30, 0, 0, time.Local),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.Local),
	}

	for _, now := range cases {
		now := now
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			t.Parallel()
			next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)
			assert.True(t, next.After(now), "next sweep must be strictly after now")
		})
	}
}

func TestNextSweepTime_ResultIsExactTime(t *testing.T) {
	t.Parallel()

	// The returned time must always land on exactly 17:30:00.000
	now := time.Date(2026, 5, 28, 1, 30, 45, 123456789, time.Local)
	next := orphan.NextSweepTime(now, 17*time.Hour+30*time.Minute)

	assert.Equal(t, 17, next.Hour())
	assert.Equal(t, 30, next.Minute())
	assert.Equal(t, 0, next.Second())
	assert.Equal(t, 0, next.Nanosecond())
}

func TestNextSweepTime_WithMinutes(t *testing.T) {
	t.Parallel()

	// Works with arbitrary hour:minute combinations
	now := time.Date(2026, 5, 28, 1, 0, 0, 0, time.Local)
	next := orphan.NextSweepTime(now, 3*time.Hour+45*time.Minute)

	// 01:00 is before 03:45 today, so next sweep is today
	want := time.Date(2026, 5, 28, 3, 45, 0, 0, time.Local)
	assert.Equal(t, want, next)
}

// ─── Config.setDefaults ───────────────────────────────────────────────────────

func TestConfigSetDefaults_EmptyConfig(t *testing.T) {
	t.Parallel()

	cfg := orphan.NewReconcilerConfig()
	// TmpDirs must be non-empty after defaults are applied
	require.NotEmpty(t, cfg.TmpDirs)
	// MinOrphanAge must be positive
	assert.Greater(t, cfg.MinOrphanAge, time.Duration(0))
}

func TestConfigSetDefaults_PreservesExplicitValues(t *testing.T) {
	t.Parallel()

	custom := orphan.NewReconcilerConfigWith([]string{"/custom/tmp"}, 2*time.Hour)
	assert.Equal(t, []string{"/custom/tmp"}, custom.TmpDirs)
	assert.Equal(t, 2*time.Hour, custom.MinOrphanAge)
}
