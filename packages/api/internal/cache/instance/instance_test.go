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
	return &InstanceInfo{
		data: Data{
			SandboxID:         "test-sandbox",
			TemplateID:        "test-template",
			ClientID:          "test-client",
			TeamID:            uuid.New(),
			StartTime:         time.Now(),
			EndTime:           time.Now().Add(time.Hour),
			MaxInstanceLength: time.Hour,
			State:             StateRunning,
		},
		mu: sync.RWMutex{},
	}
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
		{"Running to Paused", StateRunning, StateActionPause, StatePaused, false},
		{"Running to Killed", StateRunning, StateActionKill, StateKilled, false},
		{"Paused to Killed", StatePaused, StateActionKill, StateKilled, false},
		{"Killed to Paused (invalid)", StateKilled, StateActionPause, StatePaused, true},
		{"Killed to Killed (same)", StateKilled, StateActionKill, StateKilled, false},
		{"Paused to Paused (same)", StatePaused, StateActionPause, StatePaused, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := createTestInstance()
			instance.data.State = tt.fromState
			ctx := t.Context()

			done, finish, err := instance.StartRemoving(ctx, tt.stateAction)

			switch {
			case tt.shouldError:
				require.Error(t, err)
				assert.False(t, done)
				assert.Nil(t, finish)
				assert.Equal(t, tt.fromState, instance.State()) // State unchanged
			case tt.fromState == tt.expState:
				require.NoError(t, err)
				assert.True(t, done)
				assert.NotNil(t, finish)
				assert.Equal(t, tt.fromState, instance.State())
			default:
				require.NoError(t, err)
				assert.False(t, done)
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
	done, finish, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, done)
	require.NotNil(t, finish)

	// The state should be changed immediately
	assert.Equal(t, StatePaused, instance.State())

	// Simulate the actual pause operation taking time
	started := make(chan struct{})

	go func() {
		started <- struct{}{}
		time.Sleep(100 * time.Millisecond)
		// The state should still be Paused
		assert.Equal(t, StatePaused, instance.State())
		finish(nil)
	}()

	// Meanwhile, another request tries to kill the sandbox
	<-started // Ensure the pause operation has started

	start := time.Now()
	done2, finish2, err2 := instance.StartRemoving(ctx, StateActionKill)
	elapsed := time.Since(start)

	// Should have waited for the pause to complete
	assert.Greater(t, elapsed, 80*time.Millisecond)
	require.NoError(t, err2)
	assert.False(t, done2)
	assert.NotNil(t, finish2)
	assert.Equal(t, StateKilled, instance.State())

	// Complete the kill operation
	finish2(nil)
	assert.Equal(t, StateKilled, instance.State())
}

// Test concurrent requests to transition to the same state (idempotency)
func TestStartRemoving_ConcurrentSameState(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	results := make(chan struct {
		done   bool
		worked bool
	}, 3)

	// Three concurrent requests to pause the sandbox
	for i := 0; i < 3; i++ {
		go func() {
			done, finish, err := instance.StartRemoving(ctx, StateActionPause)
			if err == nil {
				if done {
					// Already done (waited for another transition)
					results <- struct {
						done   bool
						worked bool
					}{done, false}
				} else {
					// We got to perform the transition
					time.Sleep(10 * time.Millisecond)
					finish(nil)
					results <- struct {
						done   bool
						worked bool
					}{done, true}
				}
			} else {
				results <- struct {
					done   bool
					worked bool
				}{false, false}
			}
		}()
	}

	// Collect results
	performedCount := 0
	doneCount := 0
	for i := 0; i < 3; i++ {
		result := <-results
		if result.worked {
			performedCount++
		}
		if result.done {
			doneCount++
		}
	}

	// Only one should have actually performed the transition (worked)
	// But others waiting should get done=true after the transition completes
	assert.Equal(t, 1, performedCount, "Only one request should actually perform the transition")
	assert.Equal(t, 2, doneCount, "Two concurrent requests should see it's already done")
	assert.Equal(t, StatePaused, instance.State())
}

// Test transition fails and subsequent request handles it
func TestStartRemoving_Error(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// First attempt to pause
	done1, finish1, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, done1)
	require.NotNil(t, finish1)

	// Start a concurrent request that will wait for the first transition
	var done2 bool
	var err2 error
	var finish2 func(error)
	completed := make(chan bool)

	go func() {
		// This should wait for the first transition, then try to go to Killed
		done2, finish2, err2 = instance.StartRemoving(ctx, StateActionKill)
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
	assert.False(t, done2)
	assert.Nil(t, finish2)

	// From Failed state, no transitions are allowed
	done3, finish3, err3 := instance.StartRemoving(ctx, StateActionPause)
	require.Error(t, err3)
	require.ErrorIs(t, err3, failureErr)
	assert.False(t, done3)
	assert.Nil(t, finish3)

	// Trying to transition to Killed should also fail
	done4, finish4, err4 := instance.StartRemoving(ctx, StateActionKill)
	require.Error(t, err4)
	require.ErrorIs(t, err4, failureErr)
	assert.False(t, done4)
	assert.Nil(t, finish4)
}

// Test context timeout during wait
func TestStartRemoving_ContextTimeout(t *testing.T) {
	instance := createTestInstance()

	// Start a long-running transition
	done1, finish1, err := instance.StartRemoving(t.Context(), StateActionPause)
	require.NoError(t, err)
	assert.False(t, done1)
	require.NotNil(t, finish1)

	// Another request with a short timeout
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err2 := instance.StartRemoving(ctx, StateActionKill)
	elapsed := time.Since(start)

	// Should timeout after about 20ms
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), context.DeadlineExceeded.Error())
	assert.Greater(t, elapsed, 15*time.Millisecond)
	assert.Less(t, elapsed, 30*time.Millisecond)

	// Clean up
	finish1(nil)
	assert.Equal(t, StatePaused, instance.State())
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
	alreadyDone, finish, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	done := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		done <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete the transition
	finish(nil)

	// Wait should complete
	<-done
	require.NoError(t, waitErr)
}

func TestWaitForStateChange_WaitWithError(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	done := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		done <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Complete the transition with error
	testErr := assert.AnError
	finish(testErr)

	// Wait should complete with error
	<-done
	require.Error(t, waitErr)
	assert.Equal(t, testErr, waitErr)
}

func TestWaitForStateChange_ContextCancellation(t *testing.T) {
	instance := createTestInstance()
	ctx, cancel := context.WithCancel(t.Context())

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, finish)

	// Wait for state change in a goroutine
	var waitErr error
	done := make(chan bool)

	go func() {
		waitErr = instance.WaitForStateChange(ctx)
		done <- true
	}()

	// Give the goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait should complete with context error
	<-done
	require.Error(t, waitErr)
	assert.Equal(t, context.Canceled, waitErr)

	// Clean up - complete the transition
	finish(nil)
}

func TestWaitForStateChange_MultipleWaiters(t *testing.T) {
	instance := createTestInstance()
	ctx := t.Context()

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, finish)

	// Start multiple waiters
	numWaiters := 5
	errors := make([]error, numWaiters)
	var wg sync.WaitGroup

	for i := 0; i < numWaiters; i++ {
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
	for i := 0; i < numWaiters; i++ {
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
	for i := 0; i < 200; i++ {
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

						done, finish, err := instance.StartRemoving(t.Context(), stateAction)
						if err == nil && (finish != nil || done) {
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
					case 3: // Read data
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
