//go:build linux

package fc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When Firecracker has exited, a tick that was already pending must not reach
// the (now dead) API socket. Both channels are armed before every iteration so
// the select genuinely has to choose between them; repeating drives the random
// choice hard enough that an unguarded loop cannot stay silent.
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

// While the VM is alive, ticks must still trigger flushes.
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

// Flush failures while the VM is alive are still reported.
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
