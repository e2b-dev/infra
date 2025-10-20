package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	sandboxID = "test-sandbox-id"
)

var teamID = uuid.New()

func newMemoryStore() *Store {
	cache := NewStore(nil, nil)
	return cache
}

func TestReservation(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	assert.NoError(t, err)
}

func TestReservation_Exceeded(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	_, _, err = cache.Reserve(teamID.String(), "sandbox-2", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)
}

func TestReservation_SameSandbox(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)

	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	assert.NotNil(t, waitForStart)
}

func TestReservation_Release(t *testing.T) {
	cache := newMemoryStore()

	_, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	cache.Remove(teamID.String(), sandboxID)

	_, _, err = cache.Reserve(teamID.String(), sandboxID, 1)
	assert.NoError(t, err)
}

func TestReservation_ResumeAlreadyRunningSandbox(t *testing.T) {
	cache := newMemoryStore()

	data := sandbox.Sandbox{
		ClientID:   consts.ClientID,
		SandboxID:  sandboxID,
		TemplateID: "test",

		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}

	cache.Add(t.Context(), data, false)

	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	assert.NotNil(t, waitForStart)
}

func TestReservation_WaitForStart(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Wait should now complete and return the sandbox
	ctx := t.Context()
	result, err := waitForStart(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result.SandboxID)
	assert.Equal(t, expectedSbx.TemplateID, result.TemplateID)
}

func TestReservation_WaitForStartError(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish with an error
	expectedErr := assert.AnError
	finishStart(sandbox.Sandbox{}, expectedErr)

	// Wait should return the error
	ctx := t.Context()
	_, err = waitForStart(ctx)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestReservation_MultipleWaiters(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Multiple calls should all return waitForStart
	_, waitForStart1, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart1)

	_, waitForStart2, err := cache.Reserve(teamID.String(), sandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart2)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// All waiters should get the result
	ctx := t.Context()
	result1, err := waitForStart1(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result1.SandboxID)

	result2, err := waitForStart2(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result2.SandboxID)
}

func TestReservation_Remove(t *testing.T) {
	cache := newMemoryStore()

	finishStart, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Remove the reservation
	cache.Remove(teamID.String(), sandboxID)

	// Should be able to reserve again
	finishStart2, _, err := cache.Reserve(teamID.String(), sandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart2)
}

func TestReservation_MultipleTeams(t *testing.T) {
	cache := newMemoryStore()

	team1 := uuid.New()
	team2 := uuid.New()
	sandbox1 := "sandbox-1"
	sandbox2 := "sandbox-2"

	// Reserve for team1
	_, _, err := cache.Reserve(team1.String(), sandbox1, 1)
	require.NoError(t, err)

	// Should not affect team2's limit
	_, _, err = cache.Reserve(team2.String(), sandbox2, 1)
	require.NoError(t, err)

	// team1 should be at limit
	_, _, err = cache.Reserve(team1.String(), "sandbox-3", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)

	// team2 should also be at limit
	_, _, err = cache.Reserve(team2.String(), "sandbox-4", 1)
	require.Error(t, err)
	assert.IsType(t, &sandbox.LimitExceededError{}, err)
}

func TestReservation_FailedStart(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	sbxID := "failed-sandbox"

	// Reserve sandbox
	finishStart, _, err := cache.Reserve(team.String(), sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Finish with an error
	expectedErr := errors.New("start failed")
	finishStart(sandbox.Sandbox{}, expectedErr)

	// After failed start, should be able to reserve again
	finishStart2, _, err := cache.Reserve(team.String(), sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart2)
}

func TestReservation_FailedStartWithWaiters(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	sbxID := "failed-with-waiters"
	numWaiters := 10

	// First reservation
	finishStart, _, err := cache.Reserve(team.String(), sbxID, 100)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	var wg errgroup.Group
	waiters := make([]func(ctx context.Context) (sandbox.Sandbox, error), numWaiters)

	// Multiple waiters
	for i := range numWaiters {
		wg.Go(func() error {
			_, waitForStart, err := cache.Reserve(team.String(), sbxID, 100)
			if err != nil {
				return err
			}

			if waitForStart == nil {
				return errors.New("waitForStart should not be nil")
			}
			waiters[i] = waitForStart

			return nil
		})
	}

	wg.Wait()

	// Finish with an error
	expectedErr := errors.New("start failed")
	finishStart(sandbox.Sandbox{}, expectedErr)

	// All waiters should receive the error
	var wg2 sync.WaitGroup
	var errorCount atomic.Int32

	for _, waiter := range waiters {
		wg2.Add(1)
		go func(w func(ctx context.Context) (sandbox.Sandbox, error)) {
			defer wg2.Done()
			_, err := w(t.Context())
			if err != nil {
				errorCount.Add(1)
			}
		}(waiter)
	}

	wg2.Wait()
	assert.Equal(t, int32(numWaiters), errorCount.Load())
}

func TestReservation_ConcurrentReservations(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	concurrency := 100
	limit := int64(50)

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var limitExceededCount atomic.Int32

	for i := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sandboxID := fmt.Sprintf("sandbox-%d", i)
			_, _, err := cache.Reserve(team.String(), sandboxID, limit)
			if err == nil {
				successCount.Add(1)
			} else {
				var limitExceededError *sandbox.LimitExceededError
				if errors.As(err, &limitExceededError) {
					limitExceededCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// Should have exactly 50 successful reservations and 50 limit exceeded errors
	assert.Equal(t, int32(limit), successCount.Load())
	assert.Equal(t, int32(concurrency)-int32(limit), limitExceededCount.Load())
}

func TestReservation_ConcurrentSameSandbox(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	sbxID := "concurrent-sandbox"
	concurrency := 50

	var wg errgroup.Group
	var finishStartCount atomic.Int32
	var waitForStartCount atomic.Int32

	// Multiple goroutines try to reserve the same sandbox
	for range concurrency {
		wg.Go(func() error {
			finishStart, waitForStart, err := cache.Reserve(team.String(), sbxID, 10)
			if err != nil {
				return err
			}

			if finishStart != nil {
				finishStartCount.Add(1)
			}
			if waitForStart != nil {
				waitForStartCount.Add(1)
			}

			return nil
		})
	}

	wg.Wait()

	// Only one should get finishStart, all others should get waitForStart
	assert.Equal(t, int32(1), finishStartCount.Load())
	assert.Equal(t, int32(concurrency-1), waitForStartCount.Load())
}

func TestReservation_ConcurrentWaitAndFinish(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	sbxID := "wait-finish-sandbox"
	numWaiters := 20

	// First goroutine reserves
	finishStart, _, err := cache.Reserve(team.String(), sbxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	var wg errgroup.Group
	waiters := make([]func(ctx context.Context) (sandbox.Sandbox, error), numWaiters)

	// Multiple waiters
	for i := range numWaiters {
		wg.Go(func() error {
			_, waitForStart, err := cache.Reserve(team.String(), sbxID, 1)
			if err != nil {
				return err
			}

			if waitForStart == nil {
				return errors.New("waitForStart should not be nil")
			}

			waiters[i] = waitForStart
			return nil
		})
	}

	wg.Wait()

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sbxID,
		TemplateID:        "test",
		TeamID:            team,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// All waiters should receive the result
	var wg2 sync.WaitGroup
	var successCount atomic.Int32

	for _, waiter := range waiters {
		wg2.Add(1)
		go func(w func(ctx context.Context) (sandbox.Sandbox, error)) {
			defer wg2.Done()
			result, err := w(t.Context())
			if err == nil && result.SandboxID == sbxID {
				successCount.Add(1)
			}
		}(waiter)
	}

	wg2.Wait()
	assert.Equal(t, int32(numWaiters), successCount.Load())
}

func TestReservation_ConcurrentRemove(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	concurrency := 50

	var wg errgroup.Group

	// Concurrently reserve and remove sandboxes
	for i := range concurrency {
		wg.Go(func() error {
			sbxID := fmt.Sprintf("sandbox-%d", i)

			// Reserve
			_, _, err := cache.Reserve(team.String(), sbxID, 100)
			if err != nil {
				return err
			}

			// Remove
			cache.Remove(team.String(), sbxID)

			// Should be able to reserve again
			_, _, err = cache.Reserve(team.String(), sbxID, 100)
			if err != nil {
				return err
			}

			return nil
		})
	}

	err := wg.Wait()
	require.NoError(t, err)
}

func TestReservation_RaceConditionStressTest(t *testing.T) {
	cache := newMemoryStore()
	team := uuid.New()
	numOperations := 2000
	numSandboxes := 100
	limit := int64(5)

	var wg sync.WaitGroup
	var operationCount atomic.Int32

	// Mix of reserve, remove, and finish operations
	for i := range numOperations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sbxID := fmt.Sprintf("sandbox-%d", i%numSandboxes)

			switch i % 3 {
			case 0:
				// Reserve
				finishStart, waitForStart, err := cache.Reserve(team.String(), sbxID, limit)
				if err == nil {
					operationCount.Add(1)
					if finishStart != nil {
						// Immediately finish
						go func() {
							time.Sleep(time.Millisecond)
							finishStart(sandbox.Sandbox{
								SandboxID: sbxID,
								TeamID:    team,
							}, nil)
						}()
					}
					if waitForStart != nil {
						// Try to wait
						go func() {
							_, _ = waitForStart(t.Context())
						}()
					}
				} else {
					var limitExceededError *sandbox.LimitExceededError
					if errors.As(err, &limitExceededError) {
						operationCount.Add(1)
					}
				}
			case 1:
				// Remove
				cache.Remove(team.String(), sbxID)
				operationCount.Add(1)
			case 2:
				// Reserve again
				_, _, _ = cache.Reserve(team.String(), sbxID, limit)
				operationCount.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, operationCount.Load(), int32(numOperations))
}
