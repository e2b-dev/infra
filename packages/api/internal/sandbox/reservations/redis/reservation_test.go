package redis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	testSandboxID = "test-sandbox-id"
)

var testTeamID = uuid.New()

func setupTestReservationStorage(t *testing.T) (*ReservationStorage, goredis.UniversalClient) {
	t.Helper()
	client := redis_utils.SetupInstance(t)
	storage := NewReservationStorage(client)
	return storage, client
}

func TestReservation(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	finishStart, _, err := storage.Reserve(t.Context(), testTeamID, testSandboxID, 1)
	assert.NoError(t, err)
	assert.NotNil(t, finishStart)
}

func TestReservation_Exceeded(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	_, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)
	_, _, err = storage.Reserve(t.Context(), teamID, "sandbox-2", 1)
	require.ErrorAs(t, err, utils.ToPtr(&sandbox.LimitExceededError{}))
}

func TestReservation_SameSandbox(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	_, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)

	_, waitForStart, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)
	assert.NotNil(t, waitForStart)
}

func TestReservation_Release(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	_, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)
	err = storage.Release(t.Context(), teamID, testSandboxID)
	require.NoError(t, err)

	_, _, err = storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	assert.NoError(t, err)
}

func TestReservation_WaitForStart(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	finishStart, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         testSandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Wait should now complete and return the sandbox
	result, err := waitForStart(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result.SandboxID)
	assert.Equal(t, expectedSbx.TemplateID, result.TemplateID)
}

func TestReservation_WaitForStartError(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	finishStart, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Second call should return waitForStart
	_, waitForStart, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Finish with an error
	finishStart(sandbox.Sandbox{}, errors.New("start failed"))

	// Wait should return an error
	_, err = waitForStart(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start failed")
}

func TestReservation_MultipleWaiters(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	finishStart, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Multiple calls should all return waitForStart
	_, waitForStart1, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart1)

	_, waitForStart2, err := storage.Reserve(t.Context(), teamID, testSandboxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart2)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         testSandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// All waiters should get the result
	result1, err := waitForStart1(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result1.SandboxID)

	result2, err := waitForStart2(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result2.SandboxID)
}

func TestReservation_Remove(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	finishStart, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         testSandboxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Remove the reservation
	err = storage.Release(t.Context(), teamID, testSandboxID)
	require.NoError(t, err)

	// Should be able to reserve again
	finishStart2, _, err := storage.Reserve(t.Context(), teamID, testSandboxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart2)
}

func TestReservation_MultipleTeams(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	team1 := uuid.New()
	team2 := uuid.New()
	sandbox1 := "sandbox-1"
	sandbox2 := "sandbox-2"

	// Reserve for team1
	_, _, err := storage.Reserve(t.Context(), team1, sandbox1, 1)
	require.NoError(t, err)

	// Should not affect team2's limit
	_, _, err = storage.Reserve(t.Context(), team2, sandbox2, 1)
	require.NoError(t, err)

	// team1 should be at limit
	_, _, err = storage.Reserve(t.Context(), team1, "sandbox-3", 1)
	require.ErrorAs(t, err, utils.ToPtr(&sandbox.LimitExceededError{}))

	// team2 should also be at limit
	_, _, err = storage.Reserve(t.Context(), team2, "sandbox-4", 1)
	require.ErrorAs(t, err, utils.ToPtr(&sandbox.LimitExceededError{}))
}

func TestReservation_FailedStart(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "failed-sandbox"

	// Reserve sandbox
	finishStart, _, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Finish with an error — this should auto-release
	finishStart(sandbox.Sandbox{}, errors.New("start failed"))

	// After failed start, should be able to reserve again
	finishStart2, _, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart2)
}

func TestReservation_FailedStartWithWaiters(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "failed-with-waiters"
	numWaiters := 10

	// First reservation
	finishStart, _, err := storage.Reserve(t.Context(), teamID, sbxID, 100)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	var wg errgroup.Group
	waiters := make([]func(ctx context.Context) (sandbox.Sandbox, error), numWaiters)

	// Multiple waiters
	for i := range numWaiters {
		wg.Go(func() error {
			_, waitForStart, err := storage.Reserve(t.Context(), teamID, sbxID, 100)
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

	err = wg.Wait()
	require.NoError(t, err)

	// Finish with an error
	finishStart(sandbox.Sandbox{}, errors.New("start failed"))

	// All waiters should receive an error
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
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	concurrency := 100
	limit := 50

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var limitExceededCount atomic.Int32

	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sandboxID := fmt.Sprintf("sandbox-%d", idx)
			_, _, err := storage.Reserve(t.Context(), teamID, sandboxID, limit)
			if err == nil {
				successCount.Add(1)
			} else {
				var limitExceededError *sandbox.LimitExceededError
				if errors.As(err, &limitExceededError) {
					limitExceededCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	// Should have exactly 50 successful reservations and 50 limit exceeded errors
	assert.Equal(t, int32(limit), successCount.Load())
	assert.Equal(t, int32(concurrency)-int32(limit), limitExceededCount.Load())
}

func TestReservation_ConcurrentSameSandbox(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "concurrent-sandbox"
	concurrency := 50

	var wg errgroup.Group
	var finishStartCount atomic.Int32
	var waitForStartCount atomic.Int32

	// Multiple goroutines try to reserve the same sandbox
	for range concurrency {
		wg.Go(func() error {
			finishStart, waitForStart, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
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

	err := wg.Wait()
	require.NoError(t, err)

	// Only one should get finishStart, all others should get waitForStart
	assert.Equal(t, int32(1), finishStartCount.Load())
	assert.Equal(t, int32(concurrency-1), waitForStartCount.Load())
}

func TestReservation_ConcurrentWaitAndFinish(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "wait-finish-sandbox"
	numWaiters := 20

	// First goroutine reserves
	finishStart, _, err := storage.Reserve(t.Context(), teamID, sbxID, 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	var wg errgroup.Group
	waiters := make([]func(ctx context.Context) (sandbox.Sandbox, error), numWaiters)

	// Multiple waiters
	for i := range numWaiters {
		wg.Go(func() error {
			_, waitForStart, err := storage.Reserve(t.Context(), teamID, sbxID, 1)
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

	err = wg.Wait()
	require.NoError(t, err)

	// Finish the start operation
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sbxID,
		TemplateID:        "test",
		TeamID:            teamID,
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
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	concurrency := 50

	var wg errgroup.Group

	// Concurrently reserve and remove sandboxes
	for i := range concurrency {
		wg.Go(func() error {
			sbxID := fmt.Sprintf("sandbox-%d", i)

			// Reserve
			_, _, err := storage.Reserve(t.Context(), teamID, sbxID, 100)
			if err != nil {
				return err
			}

			// Remove
			err = storage.Release(t.Context(), teamID, sbxID)
			if err != nil {
				return err
			}

			// Should be able to reserve again
			_, _, err = storage.Reserve(t.Context(), teamID, sbxID, 100)
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
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	numOperations := 2000
	numSandboxes := 100
	limit := 5

	var wg sync.WaitGroup
	var operationCount atomic.Int32

	// Mix of reserve, remove, and finish operations
	for i := range numOperations {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sbxID := fmt.Sprintf("sandbox-%d", idx%numSandboxes)

			switch idx % 3 {
			case 0:
				// Reserve
				finishStart, _, err := storage.Reserve(t.Context(), teamID, sbxID, limit)
				if err == nil {
					operationCount.Add(1)
					if finishStart != nil {
						// Immediately finish
						go func() {
							time.Sleep(time.Millisecond)
							finishStart(sandbox.Sandbox{
								SandboxID: sbxID,
								TeamID:    teamID,
							}, nil)
						}()
					}
				} else {
					var limitExceededError *sandbox.LimitExceededError
					if errors.As(err, &limitExceededError) || errors.Is(err, sandbox.ErrAlreadyExists) {
						operationCount.Add(1)
					}
				}
			case 1:
				// Remove
				_ = storage.Release(t.Context(), teamID, sbxID)
				operationCount.Add(1)
			case 2:
				// Reserve again
				_, _, _ = storage.Reserve(t.Context(), teamID, sbxID, limit)
				operationCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(numOperations), operationCount.Load())
}

// Redis-specific tests

func TestReservation_CrossInstanceWait(t *testing.T) {
	t.Parallel()
	client := redis_utils.SetupInstance(t)

	// Two separate ReservationStorage instances sharing the same Redis
	storage1 := NewReservationStorage(client)
	storage2 := NewReservationStorage(client)

	teamID := uuid.New()
	sbxID := "cross-instance-sandbox"

	// Instance 1 reserves
	finishStart, _, err := storage1.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Instance 2 sees it as pending and gets waitForStart
	_, waitForStart, err := storage2.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Instance 1 finishes
	expectedSbx := sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sbxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}
	finishStart(expectedSbx, nil)

	// Instance 2 gets the result
	result, err := waitForStart(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedSbx.SandboxID, result.SandboxID)
	assert.Equal(t, expectedSbx.TemplateID, result.TemplateID)
}

func TestReservation_CrossInstanceErrorPropagation(t *testing.T) {
	t.Parallel()
	client := redis_utils.SetupInstance(t)

	storage1 := NewReservationStorage(client)
	storage2 := NewReservationStorage(client)

	teamID := uuid.New()
	sbxID := "cross-instance-error"

	// Instance 1 reserves
	finishStart, _, err := storage1.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Instance 2 waits
	_, waitForStart, err := storage2.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Instance 1 finishes with an api.APIError
	apiErr := &api.APIError{
		Code:      http.StatusTooManyRequests,
		ClientMsg: "too many sandboxes",
		Err:       errors.New("limit exceeded"),
	}
	finishStart(sandbox.Sandbox{}, apiErr)

	// Instance 2 should get back an api.APIError with the same fields
	_, err = waitForStart(t.Context())
	require.Error(t, err)

	var reconstructedErr *api.APIError
	require.ErrorAs(t, err, &reconstructedErr)
	assert.Equal(t, http.StatusTooManyRequests, reconstructedErr.Code)
	assert.Equal(t, "too many sandboxes", reconstructedErr.ClientMsg)
}

func TestReservation_StorageIndexCountsTowardLimit(t *testing.T) {
	t.Parallel()
	storage, client := setupTestReservationStorage(t)

	teamID := uuid.New()

	// Pre-populate the storage index set with 3 sandbox IDs (simulating running sandboxes)
	storageIndexKey := getStorageIndexKey(teamID.String())
	err := client.SAdd(t.Context(), storageIndexKey, "running-1", "running-2", "running-3").Err()
	require.NoError(t, err)

	// With limit 5, we should be able to reserve 2 more (3 in storage + 2 pending = 5)
	_, _, err = storage.Reserve(t.Context(), teamID, "new-1", 5)
	require.NoError(t, err)

	_, _, err = storage.Reserve(t.Context(), teamID, "new-2", 5)
	require.NoError(t, err)

	// Third should fail — 3 (storage) + 2 (pending) = 5 >= 5
	_, _, err = storage.Reserve(t.Context(), teamID, "new-3", 5)
	require.ErrorAs(t, err, utils.ToPtr(&sandbox.LimitExceededError{}))
}

func TestReservation_AlreadyInStorageIndex(t *testing.T) {
	t.Parallel()
	storage, client := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "already-running"

	// Pre-populate the storage index set
	storageIndexKey := getStorageIndexKey(teamID.String())
	err := client.SAdd(t.Context(), storageIndexKey, sbxID).Err()
	require.NoError(t, err)

	// Reserve should return AlreadyExistsError
	finishStart, waitForStart, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.ErrorIs(t, err, sandbox.ErrAlreadyExists)
	assert.Nil(t, finishStart)
	assert.Nil(t, waitForStart)
}

func TestReservation_ResultKeyTTL(t *testing.T) {
	t.Parallel()
	storage, client := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "ttl-test"

	finishStart, _, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	finishStart(sandbox.Sandbox{SandboxID: sbxID, TeamID: teamID}, nil)

	// Result key should exist with a TTL
	resultKeyStr := getResultKey(teamID.String(), sbxID)
	ttl, err := client.TTL(t.Context(), resultKeyStr).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, resultTTL)
}

func TestReservation_NoLimitBypass(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()

	// With limit=-1, should be able to reserve unlimited sandboxes
	for i := range 20 {
		finishStart, _, err := storage.Reserve(t.Context(), teamID, fmt.Sprintf("sandbox-%d", i), -1)
		require.NoError(t, err)
		require.NotNil(t, finishStart)
	}
}

func TestReservation_ContextCancellation(t *testing.T) {
	t.Parallel()
	storage, _ := setupTestReservationStorage(t)

	teamID := uuid.New()
	sbxID := "ctx-cancel-sandbox"

	// Reserve the sandbox
	_, _, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)

	// Second call gets waitForStart
	_, waitForStart, err := storage.Reserve(t.Context(), teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	// Cancel the context
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Wait should return context cancelled
	_, err = waitForStart(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReservation_StalePendingCleanup(t *testing.T) {
	t.Parallel()
	_, client := setupTestReservationStorage(t)

	teamID := uuid.New()
	pendingSetKey := getPendingSetKey(teamID.String())

	// Simulate a crashed API instance by manually inserting a stale pending entry
	// with a timestamp well in the past (3 minutes ago, beyond the 2-minute staleTTL)
	staleTimestamp := float64(time.Now().Add(-3 * time.Minute).Unix())
	err := client.ZAdd(t.Context(), pendingSetKey, goredis.Z{
		Score:  staleTimestamp,
		Member: "orphaned-sandbox",
	}).Err()
	require.NoError(t, err)

	// Verify it's there
	count, err := client.ZCard(t.Context(), pendingSetKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Create a new storage instance (simulating a fresh/restarted API)
	storage := NewReservationStorage(client)

	// Reserve with limit=1 — this should succeed because the stale entry
	// gets cleaned up by the reserveScript before counting
	finishStart, _, err := storage.Reserve(t.Context(), teamID, "new-sandbox", 1)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// The stale entry should be gone
	count, err = client.ZCard(t.Context(), pendingSetKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count) // only the new entry remains

	// Verify the orphaned sandbox is no longer in the set
	score := client.ZScore(t.Context(), pendingSetKey, "orphaned-sandbox")
	assert.ErrorIs(t, score.Err(), goredis.Nil)
}
