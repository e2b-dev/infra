package instance

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestInstance() *InstanceInfo {
	return NewInstanceInfo(Data{
		SandboxID:         "test-sandbox",
		TemplateID:        "test-template",
		ClientID:          "test-client",
		TeamID:            uuid.New(),
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
		State:             StateRunning,
	})
}

// Test basic state transitions
func TestStartRemoving_BasicTransitions(t *testing.T) {
	tests := []struct {
		name        string
		fromState   State
		stateAction StateAction
		expState    State
		shouldError bool
	}{
		{"Running to Paused", StateRunning, StateActionPause, StatePausing, false},
		{"Running to Killed", StateRunning, StateActionKill, StateKilling, false},
		{"Paused to Killed", StatePausing, StateActionKill, StateKilling, false},
		{"Killed to Paused (invalid)", StateKilling, StateActionPause, StatePausing, true},
		{"Killed to Killed (same)", StateKilling, StateActionKill, StateKilling, false},
		{"Paused to Paused (same)", StatePausing, StateActionPause, StatePausing, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := createTestInstance()
			instance._data.State = tt.fromState
			ctx := t.Context()

			alreadyDone, finish, err := instance.startRemoving(ctx, tt.stateAction)

			switch {
			case tt.shouldError:
				require.Error(t, err)
				assert.False(t, alreadyDone)
				assert.Nil(t, finish)
				assert.Equal(t, tt.fromState, instance.State()) // State unchanged
			case tt.fromState == tt.expState:
				require.NoError(t, err)
				assert.True(t, alreadyDone)
				assert.NotNil(t, finish)
				assert.Equal(t, tt.fromState, instance.State())
			default:
				require.NoError(t, err)
				assert.False(t, alreadyDone)
				assert.NotNil(t, finish)
				assert.Equal(t, tt.expState, instance.State()) // State changed immediately
				finish(nil)                                    // Complete the transition
			}
		})
	}
}

func TestStartRemoving_PauseThenKill(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Simulate a pause operation that takes time
	alreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, finish)

	// The state should be changed immediately
	assert.Equal(t, StatePausing, instance.State())

	// Simulate the actual pause operation taking time
	started := make(chan struct{})

	go func() {
		started <- struct{}{}
		time.Sleep(100 * time.Millisecond)
		// The state should still be Paused
		assert.Equal(t, StatePausing, instance.State())
		finish(nil)
	}()

	// Meanwhile, another request tries to kill the sandbox
	<-started // Ensure the pause operation has started

	start := time.Now()
	alreadyDone2, finish2, err2 := instance.startRemoving(ctx, StateActionKill)
	elapsed := time.Since(start)

	// Should have waited for the pause to complete
	assert.Greater(t, elapsed, 80*time.Millisecond)
	require.NoError(t, err2)
	assert.False(t, alreadyDone2)
	assert.NotNil(t, finish2)
	assert.Equal(t, StateKilling, instance.State())

	// Complete the kill operation
	finish2(nil)
	assert.Equal(t, StateKilling, instance.State())
}

// Test concurrent requests to transition to the same state (idempotency)
func TestStartRemoving_ConcurrentSameState(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	results := make(chan struct {
		alreadyDone bool
		worked      bool
	}, 3)

	// Three concurrent requests to pause the sandbox
	for range 3 {
		go func() {
			alreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
			if err == nil {
				if alreadyDone {
					// Already alreadyDone (waited for another transition)
					results <- struct {
						alreadyDone bool
						worked      bool
					}{alreadyDone, false}
				} else {
					// We got to perform the transition
					time.Sleep(10 * time.Millisecond)
					finish(nil)
					results <- struct {
						alreadyDone bool
						worked      bool
					}{alreadyDone, true}
				}
			} else {
				results <- struct {
					alreadyDone bool
					worked      bool
				}{false, false}
			}
		}()
	}

	// Collect results
	performedCount := 0
	alreadyDoneCount := 0
	for range 3 {
		result := <-results
		if result.worked {
			performedCount++
		}
		if result.alreadyDone {
			alreadyDoneCount++
		}
	}

	// Only one should have actually performed the transition (worked)
	// But others waiting should get alreadyDone=true after the transition completes
	assert.Equal(t, 1, performedCount, "Only one request should actually perform the transition")
	assert.Equal(t, 2, alreadyDoneCount, "Two concurrent requests should see it's already alreadyDone")
	assert.Equal(t, StatePausing, instance.State())
}

// Test transition fails and subsequent request handles it
func TestStartRemoving_Error(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// First attempt to pause
	alreadyDone1, finish1, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone1)
	require.NotNil(t, finish1)

	// Start a concurrent request that will wait for the first transition
	var alreadyDone2 bool
	var err2 error
	var finish2 func(error)
	completed := make(chan bool)

	go func() {
		// This should wait for the first transition, then try to go to Killed
		alreadyDone2, finish2, err2 = instance.startRemoving(ctx, StateActionKill)
		completed <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete first transition with error
	failureErr := errors.New("network timeout")
	finish1(failureErr)

	// Wait for second request to complete
	<-completed

	// The waiting request should have received the error from the first transition
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), failureErr.Error())
	assert.False(t, alreadyDone2)
	assert.Nil(t, finish2)

	// From Failed state, no transitions are allowed
	alreadyDone3, finish3, err3 := instance.startRemoving(ctx, StateActionPause)
	require.Error(t, err3)
	require.ErrorIs(t, err3, failureErr)
	assert.False(t, alreadyDone3)
	assert.Nil(t, finish3)

	// Trying to transition to Killed should also fail
	alreadyDone4, finish4, err4 := instance.startRemoving(ctx, StateActionKill)
	require.Error(t, err4)
	require.ErrorIs(t, err4, failureErr)
	assert.False(t, alreadyDone4)
	assert.Nil(t, finish4)
}

// Test context timeout during wait
func TestStartRemoving_ContextTimeout(t *testing.T) {
	instance := createTestInstance()

	// Start a long-running transition
	alreadyDone1, finish1, err := instance.startRemoving(t.Context(), StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone1)
	require.NotNil(t, finish1)

	// Another request with a short timeout
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err2 := instance.startRemoving(ctx, StateActionKill)
	elapsed := time.Since(start)

	// Should timeout after about 20ms
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), context.DeadlineExceeded.Error())
	assert.Greater(t, elapsed, 15*time.Millisecond)
	assert.Less(t, elapsed, 30*time.Millisecond)

	// Clean up
	finish1(nil)
	assert.Equal(t, StatePausing, instance.State())
}

func TestWaitForStateChange_NoTransition(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Should work even with canceled context - no wait needed
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	// No transition in progress, no need to wait
	err := instance.WaitForStateChange(ctx)
	require.NoError(t, err)
}

func TestWaitForStateChange_WaitForCompletion(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Start a transition
	alreadyalreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyalreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	alreadyDone := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		alreadyDone <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete the transition
	finish(nil)

	// Wait should complete
	<-alreadyDone
	require.NoError(t, waitErr)
}

func TestWaitForStateChange_WaitWithError(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Start a transition
	alreadyalreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyalreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	alreadyDone := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		alreadyDone <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete the transition with error
	testErr := assert.AnError
	finish(testErr)

	// Wait should complete with error
	<-alreadyDone
	require.Error(t, waitErr)
	assert.Equal(t, testErr, waitErr)
}

func TestWaitForStateChange_ContextCancellation(t *testing.T) {
	instance := createTestInstance()
	ctx, cancel := context.WithCancel(t.Context())

	// Start a transition
	alreadyalreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyalreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	alreadyDone := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		alreadyDone <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait should complete with context error
	<-alreadyDone
	require.Error(t, waitErr)
	assert.Equal(t, context.Canceled, waitErr)

	// Clean up - complete the transition
	finish(nil)
}

func TestWaitForStateChange_MultipleWaiters(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Start a transition
	alreadyalreadyDone, finish, err := instance.startRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyalreadyDone)
	require.NotNil(t, finish)

	// Start multiple waiters
	numWaiters := 5
	errors := make([]error, numWaiters)
	var wg sync.WaitGroup

	for i := range numWaiters {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errors[idx] = instance.WaitForStateChange(ctx)
		}(i)
	}

	// Give the goroutines time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete the transition
	finish(nil)

	// Wait for all waiters to complete
	wg.Wait()

	// All waiters should complete successfully
	for i := range numWaiters {
		require.NoError(t, errors[i])
	}
}

// Stress test with random operations
func TestConcurrency_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	instance := createTestInstance()

	duration := 100 * time.Millisecond
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Metrics
	var opsCompleted uint64
	var errorCount uint64

	// Launch workers that continuously perform random operations
	for i := range 200 {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-stopCh:
					return
				default:
					// Random operation
					switch workerID % 4 {
					case 0: // State transitions
						stateActions := []StateAction{StateActionPause, StateActionKill}
						stateAction := stateActions[rand.Intn(len(stateActions))]

						alreadyDone, finish, err := instance.startRemoving(t.Context(), stateAction)
						if err == nil && (finish != nil || alreadyDone) {
							if finish != nil {
								finish(nil)
							}
							atomic.AddUint64(&opsCompleted, 1)
						} else if err != nil {
							atomic.AddUint64(&errorCount, 1)
						}
					case 1: // Read state
						_ = instance.State()
						atomic.AddUint64(&opsCompleted, 1)
					case 2: // Wait with timeout
						waitCtx, cancel := context.WithTimeout(t.Context(), time.Microsecond*10)
						_ = instance.WaitForStateChange(waitCtx)
						cancel()
						atomic.AddUint64(&opsCompleted, 1)
					case 3: // Read _data
						_ = instance.Data()
						atomic.AddUint64(&opsCompleted, 1)
					}
				}

				if time.Now().After(deadline) {
					return
				}
			}
		}(i)
	}

	// Let it run
	time.Sleep(duration)
	close(stopCh)
	wg.Wait()

	finalOps := atomic.LoadUint64(&opsCompleted)
	finalErrors := atomic.LoadUint64(&errorCount)
	t.Logf("Stress test completed: %d operations, %d errors", finalOps, finalErrors)

	// Should have completed many operations without panic
	assert.Greater(t, finalOps, uint64(100), "Should complete many operations")
}
