//go:build linux

package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func fastPolicy() uploadRetryPolicy {
	return uploadRetryPolicy{
		totalBudget:    2 * time.Second,
		attemptTimeout: 50 * time.Millisecond,
		initialBackoff: time.Millisecond,
		maxBackoff:     5 * time.Millisecond,
		multiplier:     2,
	}
}

func TestUploadWithRetry_RetriesTransientThenSucceeds(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32

	upload := func(context.Context) error {
		if attempts.Add(1) < 3 {
			return errors.New("gcs 503 transient")
		}

		return nil
	}

	err := uploadWithRetry(context.Background(), fastPolicy(), upload, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 3, attempts.Load(), "two failures then success")
}

func TestUploadWithRetry_BudgetExhaustion(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32

	upload := func(context.Context) error {
		attempts.Add(1)

		return errors.New("persistent 503")
	}

	err := uploadWithRetry(context.Background(), fastPolicy(), upload, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, errUploadBudgetExhausted)
	assert.Greater(t, attempts.Load(), int32(1), "retried within budget")
}

func TestUploadWithRetry_PerAttemptTimeoutDoesNotAbortLoop(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32

	upload := func(ctx context.Context) error {
		if attempts.Add(1) == 1 {
			<-ctx.Done() // first attempt blows its per-attempt deadline

			return ctx.Err()
		}

		return nil // second attempt succeeds promptly
	}

	err := uploadWithRetry(context.Background(), fastPolicy(), upload, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, attempts.Load(), "per-attempt timeout must not abort the loop")
}

func TestUploadWithRetry_CapsAttemptToRemainingBudget(t *testing.T) {
	t.Parallel()

	// attemptTimeout (10s) is far larger than the total budget (100ms). A slow
	// attempt that blocks on its context must be cut off at the budget, not run
	// for the full per-attempt timeout, so the loop never overruns totalBudget.
	policy := uploadRetryPolicy{
		totalBudget:    100 * time.Millisecond,
		attemptTimeout: 10 * time.Second,
		initialBackoff: time.Millisecond,
		maxBackoff:     5 * time.Millisecond,
		multiplier:     2,
	}

	upload := func(ctx context.Context) error {
		<-ctx.Done() // never succeeds; relies on the capped deadline

		return ctx.Err()
	}

	start := time.Now()
	err := uploadWithRetry(context.Background(), policy, upload, nil)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, errUploadBudgetExhausted)
	assert.Less(t, elapsed, 2*time.Second, "must not run for the full per-attempt timeout")
}

func TestUploadWithRetry_NonRetryableStops(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32

	upload := func(context.Context) error {
		attempts.Add(1)

		return storage.ErrObjectNotExist
	}

	err := uploadWithRetry(context.Background(), fastPolicy(), upload, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrObjectNotExist)
	assert.EqualValues(t, 1, attempts.Load(), "non-retryable error stops immediately")
}

func TestUploadWithRetry_ParentCancelAborts(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	upload := func(context.Context) error {
		cancel() // simulate shutdown mid-flight

		return errors.New("failed before cancel observed")
	}

	err := uploadWithRetry(ctx, fastPolicy(), upload, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
