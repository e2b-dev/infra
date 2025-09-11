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
		dataMu: sync.RWMutex{},
	}
}

// Test basic state transitions
func TestStartRemoving_BasicTransitions(t *testing.T) {
	tests := []struct {
		name        string
		fromState   State
		removeType  RemoveType
		expState    State
		shouldError bool
	}{
		{"Running to Paused", StateRunning, RemoveTypePause, StatePaused, false},
		{"Running to Killed", StateRunning, RemoveTypeKill, StateKilled, false},
		{"Paused to Killed", StatePaused, RemoveTypeKill, StateKilled, false},
		{"Killed to Paused (invalid)", StateKilled, RemoveTypePause, StatePaused, true},
		{"Killed to Killed (same)", StateKilled, RemoveTypeKill, StateKilled, false},
		{"Paused to Paused (same)", StatePaused, RemoveTypePause, StatePaused, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := createTestInstance()
			instance.data.State = tt.fromState
			ctx := context.Background()

			done, finish, err := instance.StartRemoving(ctx, tt.removeType)

			switch {
			case tt.shouldError:
				require.Error(t, err)
				assert.False(t, done)
				assert.Nil(t, finish)
				assert.Equal(t, tt.fromState, instance.State()) // State unchanged
			case tt.fromState == tt.expState:
				require.NoError(t, err)
				assert.False(t, done)
				assert.Nil(t, finish) // No transition needed
				assert.Equal(t, tt.fromState, instance.State())
			default:
				require.NoError(t, err)
				assert.False(t, done)
				assert.NotNil(t, finish)
				assert.Equal(t, tt.expState, instance.State()) // State changed immediately
				finish(nil)                                     // Complete the transition
			}
		})
	}
}

func TestStartRemoving_RealWorldScenario(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	// Simulate a pause operation that takes time
	done, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
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
	done2, finish2, err2 := instance.StartRemoving(ctx, RemoveTypeKill)
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
	ctx := context.Background()

	results := make(chan struct {
		done   bool
		worked bool
	}, 3)

	// Three concurrent requests to pause the sandbox
	for i := 0; i < 3; i++ {
		go func() {
			done, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
			if err == nil {
				if done {
					// Already done (waited for another transition)
					results <- struct {
						done   bool
						worked bool
					}{done, false}
				} else if finish != nil {
					// We got to perform the transition
					time.Sleep(50 * time.Millisecond)
					finish(nil)
					results <- struct {
						done   bool
						worked bool
					}{done, true}
				} else {
					// No transition needed (already in target state)
					results <- struct {
						done   bool
						worked bool
					}{false, false}
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
	ctx := context.Background()

	// First attempt to pause
	done1, finish1, err := instance.StartRemoving(ctx, RemoveTypePause)
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
		done2, finish2, err2 = instance.StartRemoving(ctx, RemoveTypeKill)
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
	assert.Equal(t, failureErr, err2)
	assert.False(t, done2)
	assert.Nil(t, finish2)

	// State should be Failed after a failed transition
	assert.Equal(t, StateFailed, instance.State())

	// From Failed state, no transitions are allowed
	done3, finish3, err3 := instance.StartRemoving(ctx, RemoveTypePause)
	require.Error(t, err3)
	assert.Contains(t, err3.Error(), "invalid state transition from failed to paused")
	assert.False(t, done3)
	assert.Nil(t, finish3)

	// Trying to transition to Killed should also fail
	done4, finish4, err4 := instance.StartRemoving(ctx, RemoveTypeKill)
	require.Error(t, err4)
	assert.Contains(t, err4.Error(), "invalid state transition from failed to killed")
	assert.False(t, done4)
	assert.Nil(t, finish4)

	// State should remain Failed
	assert.Equal(t, StateFailed, instance.State())
}

// Test context timeout during wait
func TestStartRemoving_ContextTimeout(t *testing.T) {
	instance := createTestInstance()

	// Start a long-running transition
	done1, finish1, err := instance.StartRemoving(context.Background(), RemoveTypePause)
	require.NoError(t, err)
	assert.False(t, done1)
	require.NotNil(t, finish1)

	// Another request with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err2 := instance.StartRemoving(ctx, RemoveTypeKill)
	elapsed := time.Since(start)

	// Should timeout after about 20ms
	require.Error(t, err2)
	assert.Equal(t, context.DeadlineExceeded, err2)
	assert.Greater(t, elapsed, 15*time.Millisecond)
	assert.Less(t, elapsed, 30*time.Millisecond)

	// Clean up
	finish1(nil)
	assert.Equal(t, StatePaused, instance.State())
}

func TestWaitForStateChange_NoTransition(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	// Should work even with canceled context - no wait needed
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	// No transition in progress, no need to wait
	err := instance.WaitForStateChange(ctx)
	require.NoError(t, err)
}

func TestWaitForStateChange_WaitForCompletion(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
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
	ctx := context.Background()

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
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
	assert.Equal(t, StateFailed, instance.State())
}

func TestWaitForStateChange_ContextCancellation(t *testing.T) {
	instance := createTestInstance()
	ctx, cancel := context.WithCancel(context.Background())

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
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
	ctx := context.Background()

	// Start a transition
	alreadyDone, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
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

func TestStateTransitions_ComplexScenario(t *testing.T) {
	instance := createTestInstance()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Scenario: Sequential transitions with proper completion

	// First transition: Running -> Paused
	done1, finish1, err := instance.StartRemoving(ctx, RemoveTypePause)
	require.NoError(t, err)
	assert.False(t, done1)
	require.NotNil(t, finish1)

	// Start a concurrent transition that should wait
	completed := make(chan struct {
		done   bool
		err    error
		finish func(error)
	})

	go func() {
		done2, finish2, err2 := instance.StartRemoving(ctx, RemoveTypeKill)
		completed <- struct {
			done   bool
			err    error
			finish func(error)
		}{done2, err2, finish2}
	}()

	// Give the goroutine time to start waiting
	time.Sleep(5 * time.Millisecond)

	// Complete first transition
	finish1(nil)

	// Get result from waiting goroutine
	result := <-completed
	require.NoError(t, result.err)
	assert.False(t, result.done)
	assert.NotNil(t, result.finish)

	// Complete second transition
	result.finish(nil)

	// Verify final state
	assert.Equal(t, StateKilled, instance.State())

	// Try an invalid transition from Killed state
	done3, finish3, err3 := instance.StartRemoving(ctx, RemoveTypePause)
	require.Error(t, err3)
	assert.False(t, done3)
	assert.Nil(t, finish3)
	assert.Contains(t, err3.Error(), "invalid state transition")
}

// Test heavy concurrent load with multiple state transitions
func TestConcurrency_HeavyLoad(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	var wg sync.WaitGroup
	numWorkers := 50
	opsPerWorker := 20

	// Track successful transitions
	successCount := make(chan int, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			localSuccess := 0

			for j := 0; j < opsPerWorker; j++ {
				// Alternate between different operations
				switch j % 5 {
				case 0, 1: // 40% - Try state transitions
					removeType := RemoveTypePause
					if instance.State() == StatePaused {
						removeType = RemoveTypeKill
					}
					done, finish, err := instance.StartRemoving(ctx, removeType)
					if err == nil && finish != nil {
						// Simulate actual work
						time.Sleep(time.Microsecond * 100)
						finish(nil)
						localSuccess++
					} else if done {
						localSuccess++
					}
				case 2: // 20% - Read state
					_ = instance.State()
				case 3: // 20% - Wait for changes
					waitCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
					_ = instance.WaitForStateChange(waitCtx)
					cancel()
				case 4: // 20% - Get full data
					_ = instance.Data()
				}
			}
			successCount <- localSuccess
		}(i)
	}

	wg.Wait()
	close(successCount)

	// Count total successful transitions
	total := 0
	for count := range successCount {
		total += count
	}

	// At least some transitions should succeed
	assert.Positive(t, total, 0)
	t.Logf("Successfully completed %d state transitions under heavy concurrent load", total)
}

// Test that concurrent transitions to different states work correctly
func TestConcurrency_RapidStateChanges(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	// Number of concurrent state change attempts
	numAttempts := 100
	results := make(chan struct {
		success bool
		removeType RemoveType
	}, numAttempts)

	var wg sync.WaitGroup

	// Rapidly fire state change requests
	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func(attempt int) {
			defer wg.Done()

			// Pick a remove type based on attempt number
			var removeType RemoveType
			switch attempt % 2 {
			case 0:
				removeType = RemoveTypePause
			case 1:
				removeType = RemoveTypeKill
			}

			done, finish, err := instance.StartRemoving(ctx, removeType)
			if err == nil && (finish != nil || done) {
				if finish != nil {
					// Complete the transition immediately
					finish(nil)
				}
				results <- struct {
					success bool
					removeType RemoveType
				}{true, removeType}
			} else {
				results <- struct {
					success bool
					removeType RemoveType
				}{false, removeType}
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// Analyze results
	successByType := make(map[RemoveType]int)
	failureCount := 0

	for result := range results {
		if result.success {
			successByType[result.removeType]++
		} else {
			failureCount++
		}
	}

	// We should have at least one successful transition
	totalSuccess := 0
	for removeType, count := range successByType {
		t.Logf("RemoveType %v: %d successful transitions", removeType, count)
		totalSuccess += count
	}

	assert.Positive(t, totalSuccess, 0)
	t.Logf("Total: %d successful, %d failed/rejected transitions", totalSuccess, failureCount)
}

// Test concurrent pause and kill operations (most common real scenario)
func TestConcurrency_PauseVsKill(t *testing.T) {
	for round := 0; round < 10; round++ { // Run multiple rounds to catch intermittent issues
		instance := createTestInstance()
		ctx := context.Background()

		pauseDone := make(chan bool)
		killDone := make(chan bool)

		// Start pause operation
		go func() {
			done, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
			if err == nil && (finish != nil || done) {
				if finish != nil {
					time.Sleep(5 * time.Millisecond) // Simulate pause operation
					finish(nil)
				}
				pauseDone <- true
			} else {
				pauseDone <- false
			}
		}()

		// Concurrently try to kill
		go func() {
			time.Sleep(time.Millisecond) // Small delay to let pause start
			done, finish, err := instance.StartRemoving(ctx, RemoveTypeKill)
			if err == nil && (finish != nil || done) {
				if finish != nil {
					time.Sleep(3 * time.Millisecond) // Simulate kill operation
					finish(nil)
				}
				killDone <- true
			} else {
				killDone <- false
			}
		}()

		pauseSuccess := <-pauseDone
		killSuccess := <-killDone

		// One should succeed, possibly both if kill came after pause
		assert.True(t, pauseSuccess || killSuccess, "At least one operation should succeed")

		finalState := instance.State()
		assert.Contains(t, []State{StatePaused, StateKilled}, finalState)
	}
}

// Test that state transitions with errors don't cause deadlocks
func TestConcurrency_ErrorHandling(t *testing.T) {
	instance := createTestInstance()
	ctx := context.Background()

	numWorkers := 20
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			done, finish, err := instance.StartRemoving(ctx, RemoveTypePause)
			if err == nil && finish != nil {
				if workerID%3 == 0 {
					// Some workers report errors
					finish(errors.New("simulated error"))
				} else {
					// Others succeed
					finish(nil)
				}
			} else if done {
				// Already done, that's fine
			}

			// Try another transition
			done2, finish2, err2 := instance.StartRemoving(ctx, RemoveTypeKill)
			if err2 == nil && finish2 != nil {
				finish2(nil)
			} else if done2 {
				// Already done
			}
		}(i)
	}

	wg.Wait()

	// Should complete without deadlock
	// Final state could be Failed (if error), Paused, or Killed
	finalState := instance.State()
	assert.Contains(t, []State{StatePaused, StateKilled, StateFailed}, finalState)
}

// Stress test with random operations
func TestConcurrency_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	instance := createTestInstance()
	ctx := context.Background()

	duration := 100 * time.Millisecond
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Metrics
	var opsCompleted uint64
	var errorCount uint64

	// Launch workers that continuously perform random operations
	for i := 0; i < 20; i++ {
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
						removeTypes := []RemoveType{RemoveTypePause, RemoveTypeKill}
						removeType := removeTypes[rand.Intn(len(removeTypes))]

						done, finish, err := instance.StartRemoving(ctx, removeType)
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
						waitCtx, cancel := context.WithTimeout(ctx, time.Microsecond*10)
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
