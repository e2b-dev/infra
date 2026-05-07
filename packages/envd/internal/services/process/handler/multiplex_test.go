package handler

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for MultiplexedChannel fan-out, covering the fix for the goroutine
// leak that occurred when a subscriber disconnected mid-send.

const multiplexTestTimeout = 500 * time.Millisecond

// recvOrTimeout reads one value from ch or returns ok=false after timeout.
func recvOrTimeout[T any](t *testing.T, ch <-chan T, timeout time.Duration) (T, bool) {
	t.Helper()

	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(timeout):
		var zero T

		return zero, false
	}
}

// sendOrTimeout pushes v into ch or returns false after timeout.
func sendOrTimeout[T any](t *testing.T, ch chan<- T, v T, timeout time.Duration) bool {
	t.Helper()

	select {
	case ch <- v:
		return true
	case <-time.After(timeout):
		return false
	}
}

func TestMultiplexedChannel_BasicFanOut(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)

	consA, cancelA := m.Fork()
	consB, cancelB := m.Fork()
	t.Cleanup(cancelA)
	t.Cleanup(cancelB)

	var wg sync.WaitGroup
	wg.Add(2)

	gotA := make([]int, 0, 3)
	gotB := make([]int, 0, 3)

	go func() {
		defer wg.Done()
		for v := range consA {
			gotA = append(gotA, v)
		}
	}()

	go func() {
		defer wg.Done()
		for v := range consB {
			gotB = append(gotB, v)
		}
	}()

	for _, v := range []int{1, 2, 3} {
		require.True(t,
			sendOrTimeout(t, m.Source, v, multiplexTestTimeout),
			"basic fan-out should not block when subscribers drain",
		)
	}

	m.CloseSource()
	wg.Wait()

	assert.Equal(t, []int{1, 2, 3}, gotA)
	assert.Equal(t, []int{1, 2, 3}, gotB)
}

// Regression: an abandoned consumer must not wedge the fan-out loop.
func TestMultiplexedChannel_AbandonedConsumerDoesNotWedgeFanOut(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)
	t.Cleanup(func() { m.CloseSource() })

	abandoned, cancelAbandoned := m.Fork()

	// Consumer reads one value then exits, modeling a disconnected client.
	abandonReader := make(chan struct{})
	go func() {
		<-abandoned
		close(abandonReader)
	}()

	require.True(t,
		sendOrTimeout(t, m.Source, 1, multiplexTestTimeout),
		"first send should be deliverable",
	)

	select {
	case <-abandonReader:
	case <-time.After(multiplexTestTimeout):
		t.Fatal("abandoned consumer should have read its single value")
	}

	// Simulate the handler's deferred cancel after return.
	cancelDone := make(chan struct{})
	go func() {
		cancelAbandoned()
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
	case <-time.After(multiplexTestTimeout):
		t.Fatal("cancel func did not return promptly; fan-out is wedged")
	}

	// With no subscribers, the fan-out stops draining Source to apply
	// back-pressure.  After the in-flight value is consumed, Source
	// fills and further sends block.
	sent := 0
	for i := 2; i <= 8; i++ {
		if !sendOrTimeout(t, m.Source, i, 100*time.Millisecond) {
			break
		}
		sent++
	}
	// The buffer (size 1) plus at most one in-flight value means
	// at most ~2 sends succeed before blocking.
	assert.LessOrEqual(t, sent, 3,
		"fan-out should stop draining Source when no subscribers remain (back-pressure)")
}

// Regression: an abandoned consumer must not starve other subscribers.
func TestMultiplexedChannel_AbandonedConsumerDoesNotStarveOthers(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)
	t.Cleanup(func() { m.CloseSource() })

	healthy, cancelHealthy := m.Fork()
	t.Cleanup(cancelHealthy)

	healthyReceived := make(chan int, 16)
	go func() {
		for v := range healthy {
			healthyReceived <- v
		}
	}()

	abandoned, cancelAbandoned := m.Fork()
	go func() {
		<-abandoned
	}()

	require.True(t,
		sendOrTimeout(t, m.Source, 1, multiplexTestTimeout),
		"first send should pass while both consumers are active",
	)
	got, ok := recvOrTimeout(t, healthyReceived, multiplexTestTimeout)
	require.True(t, ok, "healthy subscriber should receive value 1")
	assert.Equal(t, 1, got)

	// Abandon and cancel the second subscriber.
	cancelAbandoned()

	// Healthy subscriber must keep receiving every subsequent value.
	for i := 2; i <= 6; i++ {
		require.Truef(t,
			sendOrTimeout(t, m.Source, i, multiplexTestTimeout),
			"send %d should not be back-pressured", i,
		)
		got, ok = recvOrTimeout(t, healthyReceived, multiplexTestTimeout)
		require.Truef(t, ok, "healthy subscriber should still receive value %d", i)
		assert.Equalf(t, i, got, "healthy subscriber received wrong value")
	}
}

// cancel must be idempotent and non-blocking even under producer load.
func TestMultiplexedChannel_CancelIsIdempotentAndPrompt(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](0)

	_, cancel := m.Fork()

	// Concurrently push values without anyone draining the consumer chan.
	stop := make(chan struct{})
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for {
			select {
			case <-stop:
				return
			case m.Source <- 0:
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-producerDone
		m.CloseSource()
	})

	// Give the fan-out a chance to enter a per-subscriber select.
	time.Sleep(20 * time.Millisecond)

	cancelDone := make(chan struct{})
	go func() {
		cancel()
		cancel() // idempotent
		cancel()
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
	case <-time.After(multiplexTestTimeout):
		t.Fatal("cancel func did not return promptly under producer load")
	}
}

// Closing Source must close all live subscriber channels.
func TestMultiplexedChannel_SourceCloseClosesLiveSubscribers(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)

	cons, cancel := m.Fork()
	t.Cleanup(cancel)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range cons { //nolint:revive // drain until closed
		}
	}()

	require.True(t,
		sendOrTimeout(t, m.Source, 1, multiplexTestTimeout),
		"send should succeed",
	)

	m.CloseSource()

	select {
	case <-done:
	case <-time.After(multiplexTestTimeout):
		t.Fatal("consumer's `for v := range cons` did not terminate after " +
			"Source close")
	}
}

// Fork after Source close must return a pre-closed channel.
func TestMultiplexedChannel_ForkAfterSourceCloseReturnsClosedChan(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](0)
	m.CloseSource()

	// Wait for the fan-out goroutine to observe Source close.
	deadline := time.Now().Add(multiplexTestTimeout)
	for !m.exited.Load() {
		if time.Now().After(deadline) {
			t.Fatal("fan-out did not mark itself exited after Source close")
		}
		time.Sleep(time.Millisecond)
	}

	cons, cancel := m.Fork()
	cancel() // must not panic

	_, ok := recvOrTimeout(t, cons, multiplexTestTimeout)
	assert.False(t, ok, "Fork after shutdown must return a pre-closed channel")
}

// Regression: sending to Source after the last subscriber cancelled
// must not deadlock.  This reproduces the bug where handler.Wait()
// calls `p.EndEvent.Source <- event` after the handleStart/handleConnect
// handler has returned and run `defer endCancel()`.
//
// The fix is to give EndEvent a buffer of 1 so the single send in
// Wait() always succeeds, and then call CloseSource() so the fan-out
// goroutine can exit cleanly.
func TestMultiplexedChannel_SendToSourceAfterLastCancelDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	// Mirror EndEvent: buffer=1 (the fix — was 0 before).
	m := NewMultiplexedChannel[int](1)

	_, cancel := m.Fork()

	// Cancel the only subscriber immediately, without ever
	// consuming a value.  This simulates the case where
	// handleStart returns (running `defer endCancel()`) before
	// the child process exits and Wait() tries to send the
	// EndEvent.
	cancel()

	// Give the fan-out goroutine time to re-enter receiveWhenReady,
	// observe HasSubscribers()==false, and park on <-sig.
	time.Sleep(50 * time.Millisecond)

	// Now attempt to send — this models handler.Wait()'s
	// `p.EndEvent.Source <- event`.  With buffer=1 the send
	// succeeds immediately even though no subscriber is draining.
	sent := sendOrTimeout(t, m.Source, 1, 2*time.Second)
	if !sent {
		t.Fatal("Source send deadlocked after last subscriber cancelled; " +
			"EndEvent must use buffer >= 1 so Wait() never blocks")
	}

	// CloseSource wakes the fan-out so it drains the buffered
	// value and exits.  Without this the fan-out goroutine leaks.
	m.CloseSource()
}

// Regression: CloseSource after the last subscriber is cancelled must
// not leak the fan-out goroutine.  This reproduces the pattern from
// start.go where startCancel() runs before CloseSource() (LIFO defer
// order).  Previously start.go used bare close(Source) which bypassed
// the closed flag and NotifySubscriberChange, leaving the fan-out
// goroutine parked on <-sig forever.
func TestMultiplexedChannel_CloseSourceAfterCancelDoesNotLeakFanOut(t *testing.T) { //nolint:paralleltest // relies on a stable goroutine count
	const iterations = 8

	time.Sleep(50 * time.Millisecond)
	runtime.GC() //nolint:revive // intentional: settle goroutines before measuring baseline
	before := runtime.NumGoroutine()

	for range iterations {
		m := NewMultiplexedChannel[int](0)
		_, cancel := m.Fork()

		// Simulate LIFO defer order in handleStart:
		//   1. startCancel() runs first — removes subscriber
		//   2. CloseSource() runs second (the fix — was bare close before)
		cancel()
		time.Sleep(10 * time.Millisecond) // let fan-out park on <-sig
		m.CloseSource()
	}

	// Allow goroutines to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC() //nolint:revive // intentional: help goroutines finalize
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
		if runtime.NumGoroutine() <= before+2 {
			break
		}
	}

	after := runtime.NumGoroutine()
	leaked := after - before

	assert.LessOrEqualf(t, leaked, 2,
		"goroutine count grew by %d after %d iterations of "+
			"cancel-then-CloseSource; before=%d after=%d — "+
			"fan-out goroutines are leaking", leaked, iterations, before, after,
	)
}

// Goroutine count must return to baseline after cancelled subscribers settle.
func TestMultiplexedChannel_NoGoroutineLeakOnAbandon(t *testing.T) { //nolint:paralleltest // relies on a stable goroutine count
	const wedges = 16

	time.Sleep(50 * time.Millisecond)
	runtime.GC() //nolint:revive // intentional: settle goroutines before measuring baseline
	before := runtime.NumGoroutine()

	for range wedges {
		m := NewMultiplexedChannel[int](1)
		_, cancel := m.Fork()
		// Park one value so fan-out has work, then cancel mid-iteration.
		m.Source <- 0
		cancel()
		m.CloseSource()
		m.NotifySubscriberChange()
	}

	// Allow scheduled goroutines to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC() //nolint:revive // intentional: help goroutines finalize
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
		if runtime.NumGoroutine() <= before+2 {
			break
		}
	}

	after := runtime.NumGoroutine()
	leaked := after - before
	// Small slack for runtime bookkeeping; the old bug leaked >= wedges.
	assert.LessOrEqualf(t, leaked, 2,
		"goroutine count grew by %d after %d cancelled wedges; "+
			"before=%d after=%d (expected ~0)", leaked, wedges, before, after,
	)
}
