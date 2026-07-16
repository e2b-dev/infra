//go:build linux

package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasStaleLogTimestamp(t *testing.T) {
	t.Parallel()

	lifecycleStart, err := time.Parse(envdTimestampLayout, "2026-07-16T10:00:00Z")
	require.NoError(t, err)

	t.Run("timestamp from the snapshot-restored clock is stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00.123456789Z"}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp after lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		freshRaw := lifecycleStart.Add(time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": freshRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": lifecycleStart.Format(envdTimestampLayout)}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at the cutoff is not stale", func(t *testing.T) {
		t.Parallel()

		cutoffRaw := lifecycleStart.Add(-clockSkewTolerance).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": cutoffRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp within clock-skew tolerance is not stale", func(t *testing.T) {
		t.Parallel()

		withinToleranceRaw := lifecycleStart.Add(-clockSkewTolerance / 2).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": withinToleranceRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp past the cutoff is stale", func(t *testing.T) {
		t.Parallel()

		staleRaw := lifecycleStart.Add(-clockSkewTolerance - time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": staleRaw}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("zero lifecycle start disables stale detection", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00Z"}

		assert.False(t, hasStaleLogTimestamp(payload, time.Time{}))
	})

	t.Run("missing timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"message": "hello"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("non-string timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": 12345}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("invalid timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "not-a-time"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})
}
