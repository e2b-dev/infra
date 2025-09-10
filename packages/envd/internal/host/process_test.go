package host

import (
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetProcessInfo(t *testing.T) {
	// Test with current process PID
	pid := int32(1) // Use PID 1 which should exist on most systems

	info, err := getProcessInfo(pid)
	if err != nil {
		// If PID 1 doesn't exist or we can't access it, skip the test
		t.Skipf("Cannot access PID %d: %v", pid, err)
	}

	require.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, pid, info.PID)
	assert.NotEmpty(t, info.Name)
	assert.Greater(t, int64(0), info.CreateTime)
}

func TestGetProcessInfoInvalidPID(t *testing.T) {
	// Test with an invalid PID (very high number that shouldn't exist)
	invalidPID := int32(999999999)

	info, err := getProcessInfo(invalidPID)
	require.NoError(t, err)
	assert.Nil(t, info)
}

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
	go MonitorProcesses(interval, handler)

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
	assert.Greater(t, 0, initialCount, "Should have detected some initial processes")

	// Verify that events have the correct structure
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
		case <-time.After(1 * time.Second):
			t.Fatalf("No event received in iteration %d", i+1)
		}
	}
}
