package retry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrBudgetExhausted is returned (wrapped) when fn never succeeded within
// Policy.TotalBudget.
var ErrBudgetExhausted = errors.New("retry budget exhausted")

// Policy configures Do.
type Policy struct {
	// TotalBudget bounds the wall-clock time across all attempts. Required
	// (a non-positive value makes Do fail immediately with ErrBudgetExhausted).
	TotalBudget time.Duration
	// AttemptTimeout bounds a single attempt. 0 means no per-attempt timeout —
	// each attempt is bounded only by the remaining budget.
	AttemptTimeout time.Duration
	// InitialBackoff is the wait before the first retry.
	InitialBackoff time.Duration
	// MaxBackoff caps the backoff between attempts. 0 means uncapped.
	MaxBackoff time.Duration
	// Multiplier is the exponential growth factor between attempts (< 1 is
	// treated as 1, i.e. constant backoff).
	Multiplier int
}

// Do runs fn with retries until it returns nil, retryable reports the error as
// non-retryable, the budget is exhausted, or ctx is cancelled.
//
// The whole call runs under a single budget context derived from ctx, so each
// attempt's context is capped to the remaining budget and the loop never runs
// past TotalBudget. fn must respect the context it is given.
//
// retryable classifies an error as worth retrying; a nil retryable treats every
// error as retryable. onRetry, if non-nil, is called before each backoff sleep
// with the 1-based attempt number, the upcoming backoff, and the error that
// triggered the retry.
func Do(
	ctx context.Context,
	policy Policy,
	retryable func(error) bool,
	fn func(context.Context) error,
	onRetry func(attempt int, backoff time.Duration, err error),
) error {
	budgetCtx, cancel := context.WithTimeoutCause(ctx, policy.TotalBudget, ErrBudgetExhausted)
	defer cancel()

	backoff := policy.InitialBackoff

	for attempt := 1; ; attempt++ {
		err := runAttempt(budgetCtx, policy.AttemptTimeout, fn)
		if err == nil {
			return nil
		}

		// Budget exhausted or parent cancelled: stop. Checking the budget
		// context (not err) distinguishes these from a per-attempt timeout,
		// which leaves budgetCtx alive and is retryable.
		if budgetCtx.Err() != nil {
			return stopError(budgetCtx, attempt, err)
		}

		if retryable != nil && !retryable(err) {
			return fmt.Errorf("non-retryable error after %d attempts: %w", attempt, err)
		}

		if onRetry != nil {
			onRetry(attempt, backoff, err)
		}

		select {
		case <-budgetCtx.Done():
			return stopError(budgetCtx, attempt, err)
		case <-time.After(backoff):
		}

		backoff = nextBackoff(backoff, policy.MaxBackoff, policy.Multiplier)
	}
}

// runAttempt runs a single attempt under a fresh per-attempt timeout derived
// from ctx. Because the attempt context derives from the budget context, its
// effective deadline is min(attemptTimeout, remaining budget).
func runAttempt(ctx context.Context, attemptTimeout time.Duration, fn func(context.Context) error) error {
	if attemptTimeout <= 0 {
		return fn(ctx)
	}

	attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	return fn(attemptCtx)
}

func nextBackoff(cur, maxBackoff time.Duration, multiplier int) time.Duration {
	if multiplier < 1 {
		multiplier = 1
	}

	next := cur * time.Duration(multiplier)
	if maxBackoff > 0 && next > maxBackoff {
		return maxBackoff
	}

	return next
}

// stopError maps a stopped budget context to a terminal error: budget
// exhaustion vs. parent cancellation (e.g. caller shutdown).
func stopError(budgetCtx context.Context, attempt int, lastErr error) error {
	cause := context.Cause(budgetCtx)
	if errors.Is(cause, ErrBudgetExhausted) {
		return fmt.Errorf("%w after %d attempts: %w", ErrBudgetExhausted, attempt, lastErr)
	}

	return errors.Join(lastErr, cause)
}
