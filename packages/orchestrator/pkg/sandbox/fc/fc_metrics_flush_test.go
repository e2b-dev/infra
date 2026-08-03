//go:build linux

package fc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tick already pending when exit closes must not trigger a flush. Both
// channels are ready on every iteration, so the select must choose between
// them; repetition makes an unguarded loop fail despite the random pick.
func TestRunMetricsFlushLoop_NoFlushAfterExit(t *testing.T) {
	t.Parallel()

	const iterations = 1000

	exit := make(chan struct{})
	close(exit)

	ticks := make(chan time.Time, 1)
	flushes := 0

	for range iterations {
		// Re-arm the tick; the loop may or may not have consumed the last one.
		select {
		case <-ticks:
		default:
		}
		ticks <- time.Now()

		runMetricsFlushLoop(
			t.Context(),
			exit,
			ticks,
			func(context.Context) error {
				flushes++

				return nil
			},
			func(error) { t.Error("unexpected flush error") },
		)
	}

	assert.Zero(t, flushes, "flushed Firecracker metrics after the VM had exited")
}

func TestRunMetricsFlushLoop_FlushesWhileRunning(t *testing.T) {
	t.Parallel()

	exit := make(chan struct{})
	ticks := make(chan time.Time)
	flushed := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runMetricsFlushLoop(
			t.Context(),
			exit,
			ticks,
			func(context.Context) error {
				flushed <- struct{}{}

				return nil
			},
			func(error) { t.Error("unexpected flush error") },
		)
	}()

	ticks <- time.Now()
	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("tick did not trigger a flush")
	}

	close(exit)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after exit")
	}
}

func TestRunMetricsFlushLoop_ReportsFlushError(t *testing.T) {
	t.Parallel()

	exit := make(chan struct{})
	ticks := make(chan time.Time)
	errs := make(chan error, 1)

	go runMetricsFlushLoop(
		t.Context(),
		exit,
		ticks,
		func(context.Context) error { return assert.AnError },
		func(err error) { errs <- err },
	)

	ticks <- time.Now()
	select {
	case err := <-errs:
		require.ErrorIs(t, err, assert.AnError)
	case <-time.After(5 * time.Second):
		t.Fatal("flush error was not reported")
	}

	close(exit)
}
