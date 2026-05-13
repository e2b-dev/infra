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

	close(m.Source)
	wg.Wait()

	assert.Equal(t, []int{1, 2, 3}, gotA)
	assert.Equal(t, []int{1, 2, 3}, gotB)
}

// Regression: an abandoned consumer must not wedge the fan-out loop.
func TestMultiplexedChannel_AbandonedConsumerDoesNotWedgeFanOut(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)
	t.Cleanup(func() { close(m.Source) })

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

	// Producer should still make progress through Source.
	for i := 2; i <= 8; i++ {
		require.Truef(t,
			sendOrTimeout(t, m.Source, i, multiplexTestTimeout),
			"send %d should not be back-pressured by an abandoned consumer", i,
		)
	}
}

// Regression: an abandoned consumer must not starve other subscribers.
func TestMultiplexedChannel_AbandonedConsumerDoesNotStarveOthers(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)
	t.Cleanup(func() { close(m.Source) })

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
		close(m.Source)
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

	close(m.Source)

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
	close(m.Source)

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
		close(m.Source)
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

// Regression: removing a subscriber while the fan-out is mid-iteration
// must not corrupt delivery to the remaining subscribers.  Before the
// fix, run() copied only the slice header (sharing the backing array),
// so remove()'s in-place shift could cause duplicates and skips.
// Run with -race to verify no data race.
func TestMultiplexedChannel_RemoveDuringFanOutDoesNotCorrupt(t *testing.T) {
	t.Parallel()

	const iterations = 500
	const values = 20

	for iter := range iterations {
		m := NewMultiplexedChannel[int](values)

		chA, cancelA := m.Fork()
		chB, cancelB := m.Fork()
		chC, cancelC := m.Fork()

		// Pump values into the buffered Source — all fit without blocking.
		for i := 1; i <= values; i++ {
			m.Source <- i
		}

		// A reads one value then cancels — triggers remove() while
		// the fan-out is delivering to the remaining subscribers.
		// No drain needed: after cancel the fan-out skips A via <-s.done.
		go func() {
			<-chA
			cancelA()
		}()

		// B and C drain everything.
		bDone := make(chan []int, 1)
		cDone := make(chan []int, 1)
		go func() {
			var got []int
			for v := range chB {
				got = append(got, v)
			}
			bDone <- got
		}()
		go func() {
			var got []int
			for v := range chC {
				got = append(got, v)
			}
			cDone <- got
		}()

		// Shut down. close(Source) causes run() to exit and close
		// all remaining subscriber channels (B and C), which lets
		// the drainer goroutines finish and send on bDone/cDone.
		close(m.Source)

		// Collect results. B and C channels are closed by run()'s
		// cleanup, so the drainers will terminate. Cancel only
		// after collecting to avoid racing with run()'s cleanup.
		bGot := <-bDone
		cGot := <-cDone

		cancelB()
		cancelC()

		// B and C must each receive all values exactly once.
		if len(bGot) != values {
			t.Errorf("iter %d: B got %d values, want %d: %v", iter, len(bGot), values, bGot)
		}
		if len(cGot) != values {
			t.Errorf("iter %d: C got %d values, want %d: %v", iter, len(cGot), values, cGot)
		}

		cCount := map[int]int{}
		for _, v := range cGot {
			cCount[v]++
		}
		for v, n := range cCount {
			if n > 1 {
				t.Errorf("iter %d: C got value %d %d times (duplicate delivery)", iter, v, n)
			}
		}

		if t.Failed() {
			break
		}
	}
}
