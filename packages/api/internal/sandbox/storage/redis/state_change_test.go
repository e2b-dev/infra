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
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func setupTestStorage(t *testing.T) (*Storage, redis.UniversalClient) {
	t.Helper()

	client := redis_utils.SetupInstance(t)
	storage := NewStorage(client)
	go storage.Start(t.Context())
	t.Cleanup(storage.Close)

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
		{"Running to Snapshotting", sandbox.StateRunning, sandbox.StateActionSnapshot, sandbox.StateSnapshotting, false},
		{"Pausing to Killing", sandbox.StatePausing, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Killing to Pausing (invalid)", sandbox.StateKilling, sandbox.StateActionPause, sandbox.StatePausing, true},
		{"Killing to Killing (same)", sandbox.StateKilling, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Pausing to Pausing (same)", sandbox.StatePausing, sandbox.StateActionPause, sandbox.StatePausing, false},
		{"Snapshotting to Killed", sandbox.StateSnapshotting, sandbox.StateActionKill, sandbox.StateKilling, false},
		{"Snapshotting to Paused", sandbox.StateSnapshotting, sandbox.StateActionPause, sandbox.StatePausing, false},
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

			_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: tt.stateAction})

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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
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
			_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, teamID, "non-existent", sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.Error(t, err)
	assert.False(t, alreadyDone)
	assert.Nil(t, callback)

	assert.ErrorIs(t, err, sandbox.ErrNotFound)
}

func TestStartRemoving_ContextCancellation(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)

	sbx := createTestSandbox("context-cancel")
	err := storage.Add(context.Background(), sbx)
	require.NoError(t, err)

	// Start a transition
	_, alreadyDone1, callback1, err := storage.StartRemoving(context.Background(), sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	assert.False(t, alreadyDone1)
	require.NotNil(t, callback1)

	// Another request with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, alreadyDone2, _, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	elapsed := time.Since(start)

	// Should timeout
	require.Error(t, err2)
	require.ErrorIs(t, err2, context.DeadlineExceeded)
	assert.False(t, alreadyDone2)
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(context.Background(), sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, _, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
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
	_, alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err2)
	assert.False(t, alreadyDone2, "Should not be alreadyDone since we have a new execution")
	require.NotNil(t, callback2)

	// Verify the new transition key exists
	exists, err = client.Exists(ctx, transitionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	callback2(ctx, nil)
}

// transientAction is used for all transient-transition tests.
// Snapshot is currently the only transient action; if more are added
// these tests automatically cover the shared behaviour.
var transientAction = sandbox.StateActionSnapshot

func TestStartRemoving_TransientTransition(t *testing.T) {
	t.Parallel()

	t.Run("success restores state to Running", func(t *testing.T) {
		t.Parallel()

		storage, _ := setupTestStorage(t)
		ctx := t.Context()

		sbx := createTestSandbox("transient-restore")
		require.NoError(t, storage.Add(ctx, sbx))

		_, _, finish, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: transientAction})
		require.NoError(t, err)

		finish(ctx, nil)

		got, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sandbox.StateRunning, got.State)
	})

	t.Run("failure signals success to waiters", func(t *testing.T) {
		t.Parallel()

		storage, client := setupTestStorage(t)
		ctx := t.Context()

		sbx := createTestSandbox("transient-fail-result")
		require.NoError(t, storage.Add(ctx, sbx))

		_, _, finish, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: transientAction})
		require.NoError(t, err)

		transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
		transitionID, err := client.Get(ctx, transitionKey).Result()
		require.NoError(t, err)

		finish(ctx, errors.New("operation failed"))

		// Transient transitions signal success even on failure so
		// concurrent callers (e.g. kill) can proceed.
		resultKey := getTransitionResultKey(sbx.TeamID.String(), sbx.SandboxID, transitionID)
		value, err := client.Get(ctx, resultKey).Result()
		require.NoError(t, err)
		assert.Empty(t, value, "failed transient transition should still write empty result for waiters")
	})

	t.Run("restore failure propagates to result key", func(t *testing.T) {
		t.Parallel()

		storage, client := setupTestStorage(t)
		ctx := t.Context()

		sbx := createTestSandbox("transient-restore-fail")
		require.NoError(t, storage.Add(ctx, sbx))

		_, _, finish, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: transientAction})
		require.NoError(t, err)

		// Remove the sandbox key to force restoreToRunning to fail
		client.Del(ctx, getSandboxKey(sbx.TeamID.String(), sbx.SandboxID))

		transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
		transitionID, err := client.Get(ctx, transitionKey).Result()
		require.NoError(t, err)

		finish(ctx, nil)

		resultKey := getTransitionResultKey(sbx.TeamID.String(), sbx.SandboxID, transitionID)
		value, err := client.Get(ctx, resultKey).Result()
		require.NoError(t, err)
		assert.Contains(t, value, "failed to restore sandbox to running")
	})
}

// Eviction-specific tests: verify the Eviction flag in RemoveOpts
// re-checks expiry and transition state under the distributed lock.
func TestStartRemoving_Eviction(t *testing.T) {
	t.Parallel()

	t.Run("expired sandbox with no transition is evicted", func(t *testing.T) {
		t.Parallel()

		storage, _ := setupTestStorage(t)
		ctx := context.Background()

		sbx := createTestSandbox("evict-ok")
		sbx.StartTime = time.Now().Add(-2 * time.Hour)
		sbx.EndTime = time.Now().Add(-time.Second) // already expired

		err := storage.Add(ctx, sbx)
		require.NoError(t, err)

		_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill, Eviction: true})
		require.NoError(t, err)
		assert.False(t, alreadyDone)
		require.NotNil(t, callback)

		got, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, getErr)
		assert.Equal(t, sandbox.StateKilling, got.State)

		callback(ctx, nil)
	})

	t.Run("non-expired sandbox returns eviction not needed", func(t *testing.T) {
		t.Parallel()

		storage, _ := setupTestStorage(t)
		ctx := context.Background()

		sbx := createTestSandbox("evict-not-expired")
		// EndTime defaults to 1 hour from now (not expired)

		err := storage.Add(ctx, sbx)
		require.NoError(t, err)

		_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill, Eviction: true})
		require.ErrorIs(t, err, sandbox.ErrEvictionNotNeeded)
		assert.False(t, alreadyDone)
		assert.Nil(t, callback)

		// State must remain Running — sandbox was not touched.
		got, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, getErr)
		assert.Equal(t, sandbox.StateRunning, got.State)
	})

	t.Run("expired sandbox with active transition returns eviction in progress", func(t *testing.T) {
		t.Parallel()

		storage, _ := setupTestStorage(t)
		ctx := context.Background()

		sbx := createTestSandbox("evict-in-transition")
		sbx.StartTime = time.Now().Add(-2 * time.Hour)
		sbx.EndTime = time.Now().Add(-time.Second) // expired

		err := storage.Add(ctx, sbx)
		require.NoError(t, err)

		// Start a non-eviction pause transition to occupy the transition slot.
		_, _, pauseCallback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
		require.NoError(t, err)
		require.NotNil(t, pauseCallback)

		// Eviction should be rejected immediately (not block waiting for the transition).
		start := time.Now()
		_, alreadyDone, callback, evictErr := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill, Eviction: true})
		elapsed := time.Since(start)

		require.ErrorIs(t, evictErr, sandbox.ErrEvictionInProgress)
		assert.False(t, alreadyDone)
		assert.Nil(t, callback)
		assert.Less(t, elapsed, 500*time.Millisecond, "eviction should return immediately, not wait for the transition")

		// Clean up
		pauseCallback(ctx, nil)
	})

	t.Run("expired sandbox evicted with auto-pause action", func(t *testing.T) {
		t.Parallel()

		storage, _ := setupTestStorage(t)
		ctx := context.Background()

		sbx := createTestSandbox("evict-autopause")
		sbx.StartTime = time.Now().Add(-2 * time.Hour)
		sbx.EndTime = time.Now().Add(-time.Second) // expired
		sbx.Lifecycle.AutoPause = true

		err := storage.Add(ctx, sbx)
		require.NoError(t, err)

		_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause, Eviction: true})
		require.NoError(t, err)
		assert.False(t, alreadyDone)
		require.NotNil(t, callback)

		got, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, getErr)
		assert.Equal(t, sandbox.StatePausing, got.State)

		callback(ctx, nil)
	})

	t.Run("eviction flag is not propagated on retry after waiting", func(t *testing.T) {
		t.Parallel()

		// A non-eviction kill waits for an active pause, then retries.
		// If EndTime is extended mid-flight (simulating KeepAliveFor),
		// the retry must still proceed because it is not an eviction.
		storage, _ := setupTestStorage(t)
		ctx := context.Background()

		sbx := createTestSandbox("evict-retry-no-flag")
		sbx.StartTime = time.Now().Add(-2 * time.Hour)
		sbx.EndTime = time.Now().Add(-time.Second) // expired

		err := storage.Add(ctx, sbx)
		require.NoError(t, err)

		// Start a non-eviction pause.
		_, _, pauseCallback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
		require.NoError(t, err)
		require.NotNil(t, pauseCallback)

		// A non-eviction kill will wait for the pause, then retry.
		killDone := make(chan struct{})
		var killErr error
		var killAlreadyDone bool
		var killCallback func(context.Context, error)

		go func() {
			defer close(killDone)
			_, killAlreadyDone, killCallback, killErr = storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		}()

		time.Sleep(50 * time.Millisecond)

		// Extend the sandbox timeout while the pause is in progress
		// (simulating KeepAliveFor extending EndTime).
		_, updateErr := storage.Update(ctx, sbx.TeamID, sbx.SandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
			s.EndTime = time.Now().Add(time.Hour)

			return s, nil
		})
		require.NoError(t, updateErr)

		// Complete the pause.
		pauseCallback(ctx, nil)

		<-killDone

		// The kill should succeed because it's NOT an eviction — the
		// non-expired EndTime doesn't block a regular kill.
		require.NoError(t, killErr)
		assert.False(t, killAlreadyDone)
		require.NotNil(t, killCallback)

		got, getErr := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, getErr)
		assert.Equal(t, sandbox.StateKilling, got.State)

		killCallback(ctx, nil)
	})
}

// TestWaitForStateChange_PubSubWakesWaiterFast verifies that the PubSub notification
// path (rather than the fallback 1-second ticker) wakes up the waiter promptly.
func TestWaitForStateChange_PubSubWakesWaiterFast(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("pubsub-fast-wake")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Start a waiter
	var waitErr error
	waitDone := make(chan struct{})
	waitStarted := make(chan struct{})
	go func() {
		close(waitStarted)
		waitErr = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		close(waitDone)
	}()

	// Ensure the waiter is subscribed before completing the transition
	<-waitStarted
	time.Sleep(50 * time.Millisecond)

	// Complete the transition — this publishes a PubSub notification
	start := time.Now()
	callback(ctx, nil)

	// The waiter should complete well before the 1-second poll interval
	select {
	case <-waitDone:
		elapsed := time.Since(start)
		require.NoError(t, waitErr)
		assert.Less(t, elapsed, 500*time.Millisecond,
			"waiter should be woken by PubSub much faster than the 1s poll interval")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "WaitForStateChange did not complete in time")
	}
}

// TestWaitForStateChange_MultipleWaitersPubSub verifies that multiple concurrent
// waiters are all woken promptly via the PubSub notification path.
func TestWaitForStateChange_MultipleWaitersPubSub(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("pubsub-multi-waiters")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start a transition
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	require.NotNil(t, callback)

	// Start multiple waiters
	numWaiters := 5
	errs := make([]error, numWaiters)
	completionTimes := make([]time.Duration, numWaiters)
	var wg sync.WaitGroup

	for i := range numWaiters {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = storage.WaitForStateChange(ctx, sbx.TeamID, sbx.SandboxID)
		}(i)
	}

	// Let all waiters subscribe
	time.Sleep(100 * time.Millisecond)

	// Complete the transition
	callbackTime := time.Now()
	callback(ctx, nil)

	// Wait for all
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(callbackTime)
		for i := range numWaiters {
			require.NoError(t, errs[i], "waiter %d should complete without error", i)
		}
		_ = completionTimes // used for timing assertion via elapsed
		assert.Less(t, elapsed, 500*time.Millisecond,
			"all waiters should be woken by PubSub much faster than the 1s poll interval")
	case <-time.After(3 * time.Second):
		require.FailNow(t, "not all waiters completed in time")
	}
}

// TestCallback_PublishesNotification verifies that the transition callback publishes
// a notification to the global PubSub channel with the correct routing key.
func TestCallback_PublishesNotification(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("callback-publishes")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Subscribe to the global notification channel directly
	pubsub := client.Subscribe(ctx, globalTransitionNotifyChannel)
	defer pubsub.Close()

	// Wait for the subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Start a transition
	_, _, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	require.NotNil(t, callback)

	// Read the transitionID so we can build the expected routing key
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	transitionID, err := client.Get(ctx, transitionKey).Result()
	require.NoError(t, err)

	// Complete the transition
	callback(ctx, nil)

	// Read the published message
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)

	expectedRoutingKey := getTransitionRoutingKey(sbx.TeamID.String(), sbx.SandboxID, transitionID)
	assert.Equal(t, expectedRoutingKey, msg.Payload, "published payload should be the per-transition routing key")
}

// TestStartRemoving_PauseThenKill_PubSubFastWake verifies that the PubSub path
// makes the waiting kill complete faster than it would with only polling.
func TestStartRemoving_PauseThenKill_PubSubFastWake(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := context.Background()

	sbx := createTestSandbox("pubsub-pause-kill")
	err := storage.Add(ctx, sbx)
	require.NoError(t, err)

	// Start pause
	_, _, pauseCallback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)

	// Concurrently start a kill (will wait for pause to finish)
	killDone := make(chan struct{})
	var killErr error
	var killCallback func(context.Context, error)
	go func() {
		_, _, killCallback, killErr = storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
		close(killDone)
	}()

	// Let the kill request start waiting
	time.Sleep(50 * time.Millisecond)

	// Complete the pause — PubSub should wake the kill waiter immediately
	start := time.Now()
	pauseCallback(ctx, nil)

	select {
	case <-killDone:
		elapsed := time.Since(start)
		require.NoError(t, killErr)
		require.NotNil(t, killCallback)
		assert.Less(t, elapsed, 500*time.Millisecond,
			"kill should be woken by PubSub notification, not 1s poll")
		killCallback(ctx, nil)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "kill did not complete in time")
	}
}

// TestWaitForTransition_StalePubSubNotification verifies that a PubSub notification
// from a previous transition does not wake a waiter for the current transition.
func TestWaitForTransition_StalePubSubNotification(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)

	sbx := createTestSandbox("stale-pubsub")
	require.NoError(t, storage.Add(t.Context(), sbx))

	// --- Transition A: pause ---
	_, _, callbackA, err := storage.StartRemoving(t.Context(), sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	require.NotNil(t, callbackA)

	// Get transition A's ID so we can craft its routing key later.
	transitionKey := getTransitionKey(sbx.TeamID.String(), sbx.SandboxID)
	transitionIDA, err := client.Get(t.Context(), transitionKey).Result()
	require.NoError(t, err)

	// Complete transition A.
	callbackA(t.Context(), nil)

	// Restore to Running so we can start a new transition.
	_, err = storage.Update(t.Context(), sbx.TeamID, sbx.SandboxID, func(s sandbox.Sandbox) (sandbox.Sandbox, error) {
		s.State = sandbox.StateRunning

		return s, nil
	})
	require.NoError(t, err)

	// --- Transition B: pause again ---
	_, _, callbackB, err := storage.StartRemoving(t.Context(), sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	require.NotNil(t, callbackB)

	// Start a waiter for transition B.
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- storage.WaitForStateChange(t.Context(), sbx.TeamID, sbx.SandboxID)
	}()

	// Let the waiter subscribe.
	time.Sleep(100 * time.Millisecond)

	// Publish a notification using transition A's routing key (stale).
	staleRoutingKey := getTransitionRoutingKey(sbx.TeamID.String(), sbx.SandboxID, transitionIDA)
	require.NoError(t, client.Publish(t.Context(), globalTransitionNotifyChannel, staleRoutingKey).Err())

	// Give time for the stale notification to be (not) delivered.
	time.Sleep(200 * time.Millisecond)

	// The waiter should still be blocking — stale key doesn't match.
	select {
	case err := <-waiterDone:
		require.FailNow(t, "waiter returned prematurely on stale PubSub notification", "err: %v", err)
	default:
		// OK — still waiting
	}

	// Now complete transition B.
	callbackB(t.Context(), nil)

	select {
	case err := <-waiterDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "waiter did not complete after transition B finished")
	}
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
	_, alreadyDone, callback, err := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause})
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	callback(ctx, nil)

	// Immediately try to kill - should work since pause is completed
	_, alreadyDone2, callback2, err2 := storage.StartRemoving(ctx, sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill})
	require.NoError(t, err2)
	assert.False(t, alreadyDone2)
	require.NotNil(t, callback2)

	// Verify state is now Killing
	updated, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateKilling, updated.State)

	callback2(ctx, nil)
}
