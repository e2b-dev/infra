package port

import (
	"testing"
	"time"
)

// TestScanAndBroadcastDestroyExitsPromptly verifies that Destroy() causes
// ScanAndBroadcast to return well within the scan period, not after sleeping
// through the full interval.
func TestScanAndBroadcastDestroyExitsPromptly(t *testing.T) {
	t.Parallel()

	const scanPeriod = 5 * time.Second
	const maxExitDelay = 200 * time.Millisecond

	s := NewScanner(scanPeriod)

	done := make(chan struct{})
	go func() {
		s.ScanAndBroadcast()
		close(done)
	}()

	// Let the goroutine complete its first scan and enter the select block.
	time.Sleep(50 * time.Millisecond)

	destroyedAt := time.Now()
	s.Destroy()

	select {
	case <-done:
		if elapsed := time.Since(destroyedAt); elapsed > maxExitDelay {
			t.Errorf("ScanAndBroadcast took %v to exit after Destroy; want < %v", elapsed, maxExitDelay)
		}
	case <-time.After(maxExitDelay):
		t.Fatalf("ScanAndBroadcast did not exit within %v of Destroy; scan period is %v", maxExitDelay, scanPeriod)
	}
}

// TestScanAndBroadcastDestroyBeforeSleep verifies that Destroy() is respected
// even when called before the goroutine reaches the select block.
func TestScanAndBroadcastDestroyBeforeSleep(t *testing.T) {
	t.Parallel()

	const scanPeriod = 10 * time.Second
	const maxExitDelay = 500 * time.Millisecond

	s := NewScanner(scanPeriod)

	// Destroy immediately, before ScanAndBroadcast is even called.
	s.Destroy()

	done := make(chan struct{})
	go func() {
		s.ScanAndBroadcast()
		close(done)
	}()

	select {
	case <-done:
		// Pass: exited quickly.
	case <-time.After(maxExitDelay):
		t.Fatalf("ScanAndBroadcast did not exit within %v after pre-Destroy; scan period is %v", maxExitDelay, scanPeriod)
	}
}
