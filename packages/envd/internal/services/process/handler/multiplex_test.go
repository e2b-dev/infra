package handler

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for MultiplexedChannel fan-out.

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

// An abandoned consumer must not wedge the fan-out loop.
func TestMultiplexedChannel_AbandonedConsumerDoesNotWedgeFanOut(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)

	abandoned, cancelAbandoned := m.Fork()

	// Consumer reads one value then exits.
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

	// Cancel the subscriber.
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

	// With no subscribers, back-pressure kicks in and further sends block.
	sent := 0
	for i := 2; i <= 8; i++ {
		if !sendOrTimeout(t, m.Source, i, 100*time.Millisecond) {
			break
		}
		sent++
	}
	// Buffer (size 1) plus at most one in-flight value.
	assert.LessOrEqual(t, sent, 3,
		"fan-out should stop draining Source when no subscribers remain (back-pressure)")

	m.CloseSource()

	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out did not exit after CloseSource")
	}
}

// An abandoned consumer must not starve other subscribers.
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

// Cancel must be idempotent and non-blocking even under producer load.
func TestMultiplexedChannel_CancelIsIdempotentAndPrompt(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](0)

	_, cancel := m.Fork()

	// Push values without anyone draining.
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

	select {
	case <-m.done:
	case <-time.After(multiplexTestTimeout):
		t.Fatal("fan-out did not exit after Source close")
	}

	cons, cancel := m.Fork()
	cancel() // must not panic

	_, ok := recvOrTimeout(t, cons, multiplexTestTimeout)
	assert.False(t, ok, "Fork after shutdown must return a pre-closed channel")
}

// Sending to a buffered Source after the last subscriber cancelled must
// not deadlock.  This mirrors the EndEvent pattern in handler.Wait().
func TestMultiplexedChannel_SendToSourceAfterLastCancelDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	m := NewMultiplexedChannel[int](1)

	_, cancel := m.Fork()
	cancel()

	time.Sleep(50 * time.Millisecond)

	sent := sendOrTimeout(t, m.Source, 1, 2*time.Second)
	if !sent {
		t.Fatal("Source send deadlocked after last subscriber cancelled")
	}

	m.CloseSource()

	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out did not drain buffered value and exit after CloseSource")
	}
}

// CloseSource after the last subscriber is cancelled must not leak
// the fan-out goroutine. Mirrors the LIFO defer order in handleStart.
func TestMultiplexedChannel_CloseSourceAfterCancelDoesNotLeakFanOut(t *testing.T) {
	t.Parallel()

	const iterations = 8

	for range iterations {
		m := NewMultiplexedChannel[int](0)
		_, cancel := m.Fork()

		cancel()
		time.Sleep(10 * time.Millisecond)
		m.CloseSource()

		select {
		case <-m.done:
		case <-time.After(2 * time.Second):
			t.Fatal("fan-out goroutine did not exit after cancel-then-CloseSource")
		}
	}
}

// Fan-out goroutine exits after cancelled subscribers and CloseSource.
func TestMultiplexedChannel_NoGoroutineLeakOnAbandon(t *testing.T) {
	t.Parallel()

	const wedges = 16

	for range wedges {
		m := NewMultiplexedChannel[int](1)
		_, cancel := m.Fork()
		// Park one value so fan-out has work, then cancel mid-iteration.
		m.Source <- 0
		cancel()
		m.CloseSource()

		select {
		case <-m.done:
		case <-time.After(2 * time.Second):
			t.Fatal("fan-out goroutine did not exit after cancel + CloseSource")
		}
	}
}
