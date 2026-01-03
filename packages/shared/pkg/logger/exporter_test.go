package logger

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPWriterWaitGroupReuse tests the race condition where WaitGroup is reused
// before previous Wait has returned, which should panic with:
// "sync: WaitGroup is reused before previous Wait has returned"
func TestHTTPWriterWaitGroupReuse(t *testing.T) {
	t.Parallel()
	// Create a mock HTTP server that responds slowly to increase chance of race
	requestCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		// Small delay to simulate network latency and increase race window
		time.Sleep(1 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		server.Close()
	})

	ctx := t.Context()
	writer := NewHTTPWriter(ctx, server.URL)

	// Use a channel to detect panics
	panicChan := make(chan any, 1)
	done := make(chan bool)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicChan <- r
			}
			close(done)
		}()

		// Run multiple iterations to increase likelihood of hitting the race
		for range 20 {
			var wg sync.WaitGroup

			// Spawn multiple goroutines that write concurrently
			numWriters := 5
			for i := range numWriters {
				wg.Go(func() {
					// Write multiple log lines
					for j := range 3 {
						logLine := fmt.Sprintf(`{"level":"info","msg":"test log %d-%d"}`+"\n", i, j)
						_, err := writer.Write([]byte(logLine))
						if err != nil {
							t.Errorf("Write failed: %v", err)
						}
						// Yield to increase chance of interleaving
						runtime.Gosched()
					}
				})
			}

			// Spawn multiple goroutines that call Sync() concurrently
			numSyncers := 2
			for range numSyncers {
				wg.Go(func() {
					runtime.Gosched() // Let some Write() calls happen first
					err := writer.Sync()
					if err != nil {
						t.Errorf("Sync failed: %v", err)
					}
					// Immediately try to write again after sync
					// This creates the race: Sync() is waiting on WaitGroup,
					// but Write() immediately tries to reuse it
					logLine := `{"level":"info","msg":"post-sync log"}` + "\n"
					_, err = writer.Write([]byte(logLine))
					if err != nil {
						t.Errorf("Write after sync failed: %v", err)
					}
				})
			}

			// Wait for all goroutines to complete
			wg.Wait()

			// Final sync to wait for any pending writes
			err := writer.Sync()
			if err != nil {
				t.Errorf("Final sync failed: %v", err)
			}
		}
	}()

	// Wait for test to complete or timeout
	select {
	case panicMsg := <-panicChan:
		t.Fatalf("PANIC detected: %v", panicMsg)
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Test timed out")
	}
}

// TestHTTPWriterConcurrentWriteSync tests heavy concurrent usage
// This is a more aggressive test that tries to trigger the race condition
func TestHTTPWriterConcurrentWriteSync(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		server.Close()
	})

	ctx := t.Context()
	writer := NewHTTPWriter(ctx, server.URL)

	// Detect if panic occurs
	var panicDetected atomic.Bool

	// Run the test for a short duration with moderate concurrency
	stopChan := make(chan struct{})
	var testWg sync.WaitGroup

	// Writer goroutines
	for range 5 {
		testWg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					panicDetected.Store(true)
					t.Logf("PANIC in writer: %v", r)
				}
			}()

			for {
				select {
				case <-stopChan:
					return
				default:
					logLine := `{"level":"info","msg":"test"}` + "\n"
					writer.Write([]byte(logLine))
					// Small sleep to avoid overwhelming the server
					time.Sleep(1 * time.Millisecond)
					runtime.Gosched()
				}
			}
		})
	}

	// Sync goroutines - these call Sync() repeatedly
	for range 3 {
		testWg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					panicDetected.Store(true)
					t.Logf("PANIC in syncer: %v", r)
				}
			}()

			for {
				select {
				case <-stopChan:
					return
				default:
					writer.Sync()
					// Small sleep to avoid overwhelming the server
					time.Sleep(2 * time.Millisecond)
					runtime.Gosched()
				}
			}
		})
	}

	// Let the test run for a short duration
	time.Sleep(500 * time.Millisecond)
	close(stopChan)
	testWg.Wait()

	// Final sync
	err := writer.Sync()
	if err != nil {
		t.Errorf("Final sync failed: %v", err)
	}

	if panicDetected.Load() {
		t.Fatal("Race condition detected: WaitGroup reuse panic occurred")
	}
}

// TestHTTPWriterSequentialWrites tests basic sequential write and sync
func TestHTTPWriterSequentialWrites(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		server.Close()
	})

	ctx := t.Context()
	writer := NewHTTPWriter(ctx, server.URL)

	// Write multiple log lines
	logLines := []string{
		`{"level":"info","msg":"line 1"}` + "\n",
		`{"level":"warn","msg":"line 2"}` + "\n",
		`{"level":"error","msg":"line 3"}` + "\n",
	}

	for _, line := range logLines {
		n, err := writer.Write([]byte(line))
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if n != len(line) {
			t.Errorf("Expected to write %d bytes, wrote %d", len(line), n)
		}
	}

	// Sync to ensure all writes complete
	err := writer.Sync()
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Give a moment for async operations to complete
	time.Sleep(100 * time.Millisecond)

	if requestCount.Load() != int32(len(logLines)) {
		t.Errorf("Expected %d requests, got %d", len(logLines), requestCount.Load())
	}
}
