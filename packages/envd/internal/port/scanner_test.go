package port

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestScannerSubscriber_Signal_DoesNotBlockAfterDestroy verifies that Signal()
// returns immediately when the subscriber has been destroyed, rather than
// blocking forever on the unbuffered Messages channel.
func TestScannerSubscriber_Signal_DoesNotBlockAfterDestroy(t *testing.T) {
	t.Parallel()

	l := zerolog.Nop()
	sub := NewScannerSubscriber(&l, "test", nil)
	sub.Destroy()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sub.Signal(nil) // must not block
	}()

	select {
	case <-done:
		// pass
	case <-time.After(time.Second):
		t.Fatal("Signal() blocked after Destroy() — goroutine leaked")
	}
}

// TestScanner_Destroy_ExitsAfterSubscriberStopped verifies that
// Scanner.Destroy() successfully stops ScanAndBroadcast even when a subscriber
// has been destroyed and is no longer consuming Messages.
//
// Before the fix, ScanAndBroadcast would block inside sub.Signal() after the
// subscriber stopped, making the scanExit select unreachable and causing
// Scanner.Destroy() to have no effect.
func TestScanner_Destroy_ExitsAfterSubscriberStopped(t *testing.T) {
	t.Parallel()

	l := zerolog.Nop()
	scanner := NewScanner(10 * time.Millisecond)

	sub := scanner.AddSubscriber(&l, "stopped-sub", nil)
	// Destroy the subscriber to simulate ctx cancellation in StartForwarding.
	sub.Destroy()

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner.ScanAndBroadcast()
	}()

	// Let the scanner tick at least once so it calls sub.Signal().
	time.Sleep(50 * time.Millisecond)

	// Destroy must be able to stop ScanAndBroadcast even with a stopped subscriber.
	scanner.Destroy()

	select {
	case <-scanDone:
		// pass
	case <-time.After(2 * time.Second):
		t.Fatal("ScanAndBroadcast did not exit after Scanner.Destroy() — goroutine leaked")
	}
}

// TestScanner_Destroy_NoPanicOnConcurrentSignal verifies that destroying a
// subscriber while ScanAndBroadcast is in the middle of a Signal() call does
// not panic.
//
// Before the fix, Destroy() closed Messages. If Signal() was concurrently
// sending to Messages, the close caused a "send on closed channel" panic.
// With the fix, Destroy() closes the done channel instead; Signal() selects on
// done and returns cleanly, so Messages is never sent to after close.
func TestScanner_Destroy_NoPanicOnConcurrentSignal(t *testing.T) {
	t.Parallel()

	l := zerolog.Nop()
	scanner := NewScanner(5 * time.Millisecond)

	// A subscriber whose receiver exits immediately to trigger blocking Signal().
	sub := scanner.AddSubscriber(&l, "racing-sub", nil)

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner.ScanAndBroadcast()
	}()

	// Let the scanner run a few ticks.
	time.Sleep(30 * time.Millisecond)

	// Destroy concurrently — must not panic regardless of Signal() timing.
	scanner.Unsubscribe(sub)
	scanner.Destroy()

	select {
	case <-scanDone:
		// pass — no panic
	case <-time.After(2 * time.Second):
		t.Fatal("ScanAndBroadcast did not exit after Scanner.Destroy()")
	}
}
