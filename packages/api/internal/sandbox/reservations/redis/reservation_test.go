package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
	require.NoError(t, err)
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

// TestReservation_TraceKeyWrittenAndReadByWaiter verifies the cross-instance
// linking path: the primary's traceparent lands in Redis, a waiter reads it,
// and the waiter's span gets a link back to the primary. finishStart must
// delete the trace key afterwards.
func TestReservation_TraceKeyWrittenAndReadByWaiter(t *testing.T) {
	t.Parallel()

	// Use the production propagator so Inject/Extract shapes match prod.
	otel.SetTextMapPropagator(telemetry.NewTextPropagator())

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("test")

	storage, client := setupTestReservationStorage(t)
	teamID := uuid.New()
	sbxID := "trace-link-sandbox"

	// Primary reservation: a real span is attached so the Lua script writes
	// the trace key.
	primaryCtx, primary := tracer.Start(t.Context(), "primary")
	primarySC := primary.SpanContext()
	finishStart, _, err := storage.Reserve(primaryCtx, teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, finishStart)

	// Trace key should exist and hold a non-empty W3C traceparent.
	traceKeyStr := getTraceKey(teamID.String(), sbxID)
	traceparent, err := client.Get(t.Context(), traceKeyStr).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, traceparent)

	// The second reserver hits the already-pending branch and gets a waiter
	// closure that reads the trace key and links before polling.
	waiterCtx, waiter := tracer.Start(t.Context(), "waiter")
	_, waitForStart, err := storage.Reserve(waiterCtx, teamID, sbxID, 10)
	require.NoError(t, err)
	require.NotNil(t, waitForStart)

	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		_, _ = waitForStart(waiterCtx)
	}()
	// Give the waiter a moment to read the trace key and add the link.
	time.Sleep(50 * time.Millisecond)
	waiter.End()

	// Finish the primary reservation so the waiter goroutine returns.
	finishStart(sandbox.Sandbox{
		ClientID:          consts.ClientID,
		SandboxID:         sbxID,
		TemplateID:        "test",
		TeamID:            teamID,
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		MaxInstanceLength: time.Hour,
	}, nil)
	<-waiterDone

	// The waiter's span must have one link to the primary's span.
	var waiterSpan tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.Name == "waiter" {
			waiterSpan = s
		}
	}
	require.Len(t, waiterSpan.Links, 1)
	assert.Equal(t, primarySC.TraceID(), waiterSpan.Links[0].SpanContext.TraceID())
	assert.Equal(t, primarySC.SpanID(), waiterSpan.Links[0].SpanContext.SpanID())

	// And the trace key must be cleaned up by finishStartScript.
	_, err = client.Get(t.Context(), traceKeyStr).Result()
	assert.ErrorIs(t, err, goredis.Nil, "trace key should be deleted on finishStart")
}
