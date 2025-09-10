package host

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitorProcesses(t *testing.T) {
	// Create a channel to collect events
	events := make(chan *ProcessInfo, 100)
	var mu sync.Mutex
	var eventCount int

	// Event handler that captures events
	handler := func(processInfo *ProcessInfo) error {
		mu.Lock()
		eventCount++
		mu.Unlock()
		events <- processInfo
		return nil
	}

	// Start monitoring with a short interval
	interval := 100 * time.Millisecond
	logger := zerolog.New(os.Stdout).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	go MonitorProcesses(&logger, interval, handler)

	// Run a short-lived process: sleep 1
	cmdName := "sleep"
	cmd := exec.Command(cmdName, "1")
	err := cmd.Start()
	require.NoError(t, err, "Failed to start 'sleep 1' process")
	sleepPID := int32(cmd.Process.Pid)

	// Wait for the process to finish in the background
	go func() {
		_ = cmd.Wait()
	}()

	// Wait a bit for the initial scan
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	initialCount := eventCount
	mu.Unlock()

	// We should have detected some processes as running initially
	assert.Positive(t, initialCount, "Should have detected some initial processes")

	// Verify that events have the correct structure
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			expectedState := ProcessStateRunning
			if i == 1 {
				expectedState = ProcessStateExited
			}

			select {
			case event := <-events:
				if event.Name != cmdName {
					// skip the event until we get the correct one
					i--
					continue
				}
				assert.Equal(t, sleepPID, event.PID)
				assert.Equal(t, expectedState, event.State)
			}
		}
		close(done)
	}()

	select {
	case <-done:
		// Test completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Test took longer than 5 seconds to complete")
	}
}
