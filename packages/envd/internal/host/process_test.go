package host

import (
	"context"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go MonitorProcesses(ctx, &logger, interval, handler)

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
	count := 0
	go func() {
		for event := range events {
			if count >= 2 {
				close(done)
				return
			}
			if event.Name == cmdName {
				count++
				assert.Equal(t, sleepPID, event.PID)
				assert.Contains(t, []ProcessState{ProcessStateRunning, ProcessStateExited}, event.State)
			}
		}
		close(done)
	}()

	select {
	case <-done:
		// Test completed successfully
		return
	case <-time.After(5 * time.Second):
		t.Fatal("Test took longer than 5 seconds to complete")
	}
}
