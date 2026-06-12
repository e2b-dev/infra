package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastPolicy() Policy {
	return Policy{
		TotalBudget:    2 * time.Second,
		AttemptTimeout: 50 * time.Millisecond,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Multiplier:     2,
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	fn := func(context.Context) error {
		if attempts.Add(1) < 3 {
			return errors.New("transient")
		}

		return nil
	}

	require.NoError(t, Do(context.Background(), fastPolicy(), nil, fn, nil))
	assert.EqualValues(t, 3, attempts.Load())
}

func TestDo_BudgetExhausted(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	fn := func(context.Context) error {
		attempts.Add(1)

		return errors.New("persistent")
	}

	err := Do(context.Background(), fastPolicy(), nil, fn, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBudgetExhausted)
	assert.Greater(t, attempts.Load(), int32(1))
}

func TestDo_PerAttemptTimeoutDoesNotAbortLoop(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	fn := func(ctx context.Context) error {
		if attempts.Add(1) == 1 {
			<-ctx.Done() // first attempt blows its per-attempt deadline

			return ctx.Err()
		}

		return nil
	}

	require.NoError(t, Do(context.Background(), fastPolicy(), nil, fn, nil))
	assert.EqualValues(t, 2, attempts.Load())
}

func TestDo_CapsAttemptToRemainingBudget(t *testing.T) {
	t.Parallel()

	// AttemptTimeout (10s) far exceeds the budget (100ms): a blocking attempt
	// must be cut off at the budget, not run for the full per-attempt timeout.
	policy := Policy{
		TotalBudget:    100 * time.Millisecond,
		AttemptTimeout: 10 * time.Second,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Multiplier:     2,
	}

	fn := func(ctx context.Context) error {
		<-ctx.Done()

		return ctx.Err()
	}

	start := time.Now()
	err := Do(context.Background(), policy, nil, fn, nil)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, ErrBudgetExhausted)
	assert.Less(t, elapsed, 2*time.Second)
}

func TestDo_NonRetryableStops(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("permanent")
	var attempts atomic.Int32
	fn := func(context.Context) error {
		attempts.Add(1)

		return sentinel
	}
	retryable := func(err error) bool { return !errors.Is(err, sentinel) }

	err := Do(context.Background(), fastPolicy(), retryable, fn, nil)
	require.ErrorIs(t, err, sentinel)
	assert.EqualValues(t, 1, attempts.Load())
}

func TestDo_ParentCancelAborts(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	fn := func(context.Context) error {
		cancel()

		return errors.New("failed before cancel observed")
	}

	err := Do(ctx, fastPolicy(), nil, fn, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrBudgetExhausted)
}

func TestDo_NoAttemptTimeoutUsesBudget(t *testing.T) {
	t.Parallel()

	// AttemptTimeout == 0: the attempt is bounded only by the remaining budget.
	policy := Policy{
		TotalBudget:    80 * time.Millisecond,
		AttemptTimeout: 0,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Multiplier:     2,
	}

	fn := func(ctx context.Context) error {
		<-ctx.Done()

		return ctx.Err()
	}

	start := time.Now()
	err := Do(context.Background(), policy, nil, fn, nil)

	require.ErrorIs(t, err, ErrBudgetExhausted)
	assert.Less(t, time.Since(start), time.Second)
}

func TestDo_OnRetryInvoked(t *testing.T) {
	t.Parallel()

	var retries atomic.Int32
	var attempts atomic.Int32
	fn := func(context.Context) error {
		if attempts.Add(1) < 3 {
			return errors.New("transient")
		}

		return nil
	}
	onRetry := func(int, time.Duration, error) { retries.Add(1) }

	require.NoError(t, Do(context.Background(), fastPolicy(), nil, fn, onRetry))
	assert.EqualValues(t, 2, retries.Load(), "onRetry fires once per retry (not the final success)")
}
