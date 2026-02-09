package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func setupTestStorage(t *testing.T) (*Storage, redis.UniversalClient) {
	t.Helper()

	client := redis_utils.SetupInstance(t)
	storage := NewStorage(client)

	return storage, client
}

func createTestSandbox(sandboxID string) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID:         sandboxID,
		TemplateID:        "test-template",
		ClientID:          "test-client",
		TeamID:            uuid.New(),
		ExecutionID:       uuid.New().String(), // Add ExecutionID for transition tracking
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
		State:             sandbox.StateRunning,
	}
}

// Test basic state transitions
func TestStartRemoving_BasicTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fromState   sandbox.State
		stateAction sandbox.StateAction
		expState    sandbox.State
		shouldError bool
	}{
		{"Running to Pausing", sandbox.StateRunning, sandbox.StateActionPause, sandbox.StatePausing, false},
		{"Running to Killing", sandbox.StateRunning, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Pausing to Killing", sandbox.StatePausing, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Killing to Pausing (invalid)", sandbox.StateKilling, sandbox.StateActionPause, sandbox.StatePausing, true},
		{"Killing to Killing (same)", sandbox.StateKilling, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Pausing to Pausing (same)", sandbox.StatePausing, sandbox.StateActionPause, sandbox.StatePausing, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage, _ := setupTestStorage(t)
			ctx := context.Background()

			sbx := createTestSandbox("test-" + tt.name)
			sbx.State = tt.fromState

			err := storage.Add(ctx, sbx)
			require.NoError(t, err)

			alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, tt.stateAction)

			switch {
			case tt.shouldError:
				require.Error(t, err)
				assert.False(t, alreadyDone)
				assert.Nil(t, callback)
				// State should be unchanged
				updated, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
				require.NoError(t, getErr)
				assert.Equal(t, tt.fromState, updated.State)
			case tt.fromState == tt.expState:
				require.NoError(t, err)
				assert.True(t, alreadyDone)
				assert.NotNil(t, callback)
				// State unchanged (already in target state)
				updated, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
				require.NoError(t, getErr)
				assert.Equal(t, tt.fromState, updated.State)
			default:
				require.NoError(t, err)
				assert.False(t, alreadyDone)
				assert.NotNil(t, callback)
				// State changed immediately
				updated, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
				require.NoError(t, getErr)
				assert.Equal(t, tt.expState, updated.State)
				// Complete the transition
				callback(ctx, nil)
			}
		})
	}
}

func TestStartRemoving_PauseThenKill(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("pause-then-kill")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start pause operation
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// State should be changed immediately
	updated, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatePausing, updated.State)

	// Simulate the actual pause operation taking time
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		close(started)
		time.Sleep(50 * time.Millisecond)
		callback(ctx, nil)
		close(done)
	}()

	// Wait for pause operation to start
	<-started

	// Meanwhile, another request tries to kill the sandbox
	start := time.Now()
	alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionKill)
	elapsed := time.Since(start)

	// Should have waited for the pause to complete
	assert.Greater(t, elapsed, 30*time.Millisecond)
	require.NoError(t, err2)
	assert.False(t, alreadyDone2)
	assert.NotNil(t, callback2)

	// State should now be Killing
	updated, err = storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateKilling, updated.State)

	// Complete the kill operation
	callback2(ctx, nil)

	// Wait for original pause goroutine
	<-done
}

// Test concurrent requests to transition to the same state (idempotency)
func TestStartRemoving_ConcurrentSameState(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("concurrent-same")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	results := make(chan struct {
		alreadyDone bool
		worked      bool
		err         error
	}, 3)

	// Three concurrent requests to pause the sandbox
	for range 3 {
		go func() {
			alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
			if err != nil {
				results <- struct {
					alreadyDone bool
					worked      bool
					err         error
				}{false, false, err}

				return
			}
			if alreadyDone {
				// Already done (waited for another transition)
				results <- struct {
					alreadyDone bool
					worked      bool
					err         error
				}{true, false, nil}
			} else {
				// We got to perform the transition
				time.Sleep(20 * time.Millisecond)
				callback(ctx, nil)
				results <- struct {
					alreadyDone bool
					worked      bool
					err         error
				}{false, true, nil}
			}
		}()
	}

	// Collect results
	performedCount := 0
	alreadyDoneCount := 0
	errCount := 0
	for range 3 {
		result := <-results
		if result.err != nil {
			errCount++
		}
		if result.worked {
			performedCount++
		}
		if result.alreadyDone {
			alreadyDoneCount++
		}
	}

	// Only one should have actually performed the transition
	assert.Equal(t, 1, performedCount, "Only one request should actually perform the transition")
	assert.Equal(t, 2, alreadyDoneCount, "Two concurrent requests should see it's already done")
	assert.Equal(t, 0, errCount, "No errors should occur")

	// Final state should be Pausing
	updated, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatePausing, updated.State)
}

func TestStartRemoving_NotFound(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	teamID := uuid.New()
	alreadyDone, callback, err := storage.StartRemoving(ctx, teamID, "non-existent", sandbox.StateActionKill)
	require.Error(t, err)
	assert.False(t, alreadyDone)
	assert.Nil(t, callback)

	var notFoundErr *sandbox.NotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
}

func TestStartRemoving_ContextCancellation(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)

	sbx := createTestSandbox("context-cancel")
	err := storage.Add(context.Background(), sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone1, callback1, err := storage.StartRemoving(context.Background(), sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone1)
	require.NotNil(t, callback1)

	// Another request with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionKill)
	elapsed := time.Since(start)

	// Should timeout
	require.Error(t, err2)
	require.ErrorIs(t, err2, context.DeadlineExceeded)
	assert.Greater(t, elapsed, 20*time.Millisecond)
	assert.Less(t, elapsed, 200*time.Millisecond)

	// Clean up
	callback1(context.Background(), nil)
}

func TestWaitForStateChange_NoTransition(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("no-transition")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// No transition in progress, should return immediately
	err = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
}

func TestWaitForStateChange_WaitForCompletion(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("wait-completion")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Wait for state change in a goroutine
	var waitErr error
	done := make(chan bool)

	go func() {
		waitErr = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		close(done)
	}()

	// Give the goroutine time to start waiting
	time.Sleep(30 * time.Millisecond)

	// Complete the transition
	callback(ctx, nil)

	// Wait should complete
	select {
	case <-done:
		require.NoError(t, waitErr)
	case <-time.After(time.Second):
		t.Fatal("WaitForStateChange did not complete in time")
	}
}

func TestWaitForStateChange_ContextCancellation(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)

	sbx := createTestSandbox("wait-cancel")
	err := storage.Add(context.Background(), sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(context.Background(), sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Wait with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	var waitErr error
	done := make(chan bool)

	go func() {
		waitErr = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		close(done)
	}()

	// Wait should timeout
	select {
	case <-done:
		require.Error(t, waitErr)
		require.ErrorIs(t, waitErr, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("WaitForStateChange did not timeout as expected")
	}

	// Clean up
	callback(context.Background(), nil)
}

func TestWaitForStateChange_MultipleWaiters(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("multi-waiters")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Start multiple waiters
	numWaiters := 5
	errs := make([]error, numWaiters)
	var wg sync.WaitGroup

	for i := range numWaiters {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		}(i)
	}

	// Give the goroutines time to start waiting
	time.Sleep(30 * time.Millisecond)

	// Complete the transition
	callback(ctx, nil)

	// Wait for all waiters to complete
	wg.Wait()

	// All waiters should complete successfully
	for i := range numWaiters {
		require.NoError(t, errs[i])
	}
}

func TestStartRemoving_TransitionKeyTTL(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("ttl-test")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition but don't complete it
	alreadyDone, _, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)

	// Check transition key exists
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	exists, err := client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	// Check TTL is set
	ttl, err := client.TTL(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, transitionKeyTTL)
}

func TestStartRemoving_CallbackMarksTransitionCompleted(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("callback-complete")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Check transition key exists and get the transition ID
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	transitionID, err := client.Get(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, transitionID, "Transition ID should be stored in transition key")

	// Complete the transition successfully
	callback(ctx, nil)

	// Transition key should be deleted
	exists, err := client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Result key should have empty value (success) with TTL
	resultKey := getTransitionResultKey(sbx.TeamID.String(), sbx.SandboxID, transitionID)
	value, err := client.Get(ctx, resultKey).Result()
	require.NoError(t, err)
	assert.Empty(t, value, "Result should be empty string for success")

	ttl, err := client.TTL(ctx, resultKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, transitionResultKeyTTL)
}

func TestStartRemoving_CallbackSetsErrorOnFailure(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("callback-error")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Check transition key exists and get the transition ID
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	transitionID, err := client.Get(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, transitionID, "Transition ID should be stored in transition key")

	// Complete the transition with error
	callback(ctx, errors.New("test error"))

	// Transition key should be deleted
	exists, err := client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "Transition key should be deleted after callback")

	// Result key should contain error message with TTL
	resultKey := getTransitionResultKey(sbx.TeamID.String(), sbx.SandboxID, transitionID)
	value, err := client.Get(ctx, resultKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "test error", value, "Result should contain error message")

	ttl, err := client.TTL(ctx, resultKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, transitionResultKeyTTL)
}

func TestStartRemoving_SetsEndTimeWhenNotExpired(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("end-time-test")
	sbx.EndTime = time.Now().Add(time.Hour) // Not expired
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	beforeTransition := time.Now()

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionKill)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	afterTransition := time.Now()

	// Check that EndTime was set to approximately now
	updated, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.True(t, updated.EndTime.After(beforeTransition.Add(-time.Second)))
	assert.True(t, updated.EndTime.Before(afterTransition.Add(time.Second)))

	callback(ctx, nil)
}

func TestStartRemoving_WaiterCompletesOnCallbackSuccess(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("waiter-complete-success")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Start a waiter
	var waitErr error
	done := make(chan bool)
	go func() {
		waitErr = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		close(done)
	}()

	// Give the waiter time to start
	time.Sleep(30 * time.Millisecond)

	// Complete the transition successfully
	callback(ctx, nil)

	// Waiter should complete without error
	select {
	case <-done:
		require.NoError(t, waitErr)
	case <-time.After(time.Second):
		t.Fatal("WaitForStateChange did not complete in time")
	}

	// Retry should work now - sandbox is already in pausing state
	alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err2)
	// Already in pausing state from first transition
	assert.True(t, alreadyDone2)
	assert.NotNil(t, callback2)
}

func TestStartRemoving_WaiterReceivesErrorOnCallbackFailure(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("waiter-complete-error")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Start a waiter
	var waitErr error
	done := make(chan bool)
	go func() {
		waitErr = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		close(done)
	}()

	// Give the waiter time to start
	time.Sleep(30 * time.Millisecond)

	// Complete the transition with error
	callback(ctx, errors.New("connection refused"))

	// Waiter should receive the error
	select {
	case <-done:
		require.Error(t, waitErr)
		assert.Contains(t, waitErr.Error(), "connection refused")
	case <-time.After(time.Second):
		t.Fatal("WaitForStateChange did not complete in time")
	}
}

// TestStartRemoving_DifferentExecutionID tests that a new execution (after resume)
// can start a new transition after the old one completed.
func TestStartRemoving_DifferentExecutionID(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("exec-id-test")
	sbx.State = sandbox.StateRunning
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Complete it
	callback(ctx, nil)

	// Verify transition key is deleted on success
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	exists, err := client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Simulate resume: update sandbox with new ExecutionID and Running state
	_, err = storage.Update(ctx, sbx.TeamID, sbx.SandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
		s.State = sandbox.StateRunning

		return s, nil
	})
	require.NoError(t, err)

	// Now start a new pause transition - should work since previous transition completed
	alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err2)
	assert.False(t, alreadyDone2, "Should not be alreadyDone since we have a new execution")
	require.NotNil(t, callback2)

	// Verify the new transition key exists
	exists, err = client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	callback2(ctx, nil)
}

// TestStartRemoving_CompletedTransitionAllowsNewTransition tests that a completed
// transition doesn't block a new transition to a different state.
func TestStartRemoving_CompletedTransitionAllowsNewTransition(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("completed-allows-new")
	sbx.State = sandbox.StateRunning
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start and complete a pause transition
	alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionPause)
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	callback(ctx, nil)

	// Immediately try to kill - should work since pause is completed
	alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.StateActionKill)
	require.NoError(t, err2)
	assert.False(t, alreadyDone2)
	require.NotNil(t, callback2)

	// Verify state is now Killing
	updated, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateKilling, updated.State)

	callback2(ctx, nil)
}
