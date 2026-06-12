//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// errUploadBudgetExhausted is returned when a snapshot upload could not be made
// durable within the retry budget.
var errUploadBudgetExhausted = errors.New("snapshot upload budget exhausted")

// uploadRetryPolicy is the retry configuration as data: the total wall-clock
// budget, the per-attempt timeout, and the exponential backoff between
// attempts. Kept free of clocks/IO so the loop is unit-testable with
// millisecond values.
type uploadRetryPolicy struct {
	totalBudget    time.Duration // wall-clock budget across all attempts
	attemptTimeout time.Duration // fresh per-attempt deadline
	initialBackoff time.Duration
	maxBackoff     time.Duration
	multiplier     int
}

func defaultUploadRetryPolicy() uploadRetryPolicy {
	return uploadRetryPolicy{
		totalBudget:    uploadTotalBudget,
		attemptTimeout: uploadTimeout,
		initialBackoff: uploadRetryInitialBackoff,
		maxBackoff:     uploadRetryMaxBackoff,
		multiplier:     uploadRetryBackoffMultiplier,
	}
}

// uploadWithRetry retries upload until it lands durably, the budget is
// exhausted, the error is non-retryable, or the parent context is cancelled.
// Each attempt gets a FRESH per-attempt timeout so a single slow attempt never
// poisons later ones.
//
// Re-running upload is safe: it targets content-addressed storage and the
// header swap is idempotent, so a retry simply re-uploads whatever didn't land.
func uploadWithRetry(
	ctx context.Context,
	policy uploadRetryPolicy,
	upload func(ctx context.Context) error,
	onRetry func(attempt int, backoff time.Duration, err error),
) error {
	deadline := time.Now().Add(policy.totalBudget)
	backoff := policy.initialBackoff

	var lastErr error
	for attempt := 1; ; attempt++ {
		// Fresh per-attempt context: an independent deadline that does not
		// poison subsequent attempts, still cancelled by the parent context.
		attemptCtx, cancel := context.WithTimeout(ctx, policy.attemptTimeout)
		lastErr = upload(attemptCtx)
		cancel()

		if lastErr == nil {
			return nil
		}

		// Parent cancelled (shutdown): stop immediately.
		if ctx.Err() != nil {
			return errors.Join(lastErr, context.Cause(ctx))
		}

		if !isRetryableUploadErr(lastErr) {
			return fmt.Errorf("non-retryable snapshot upload error after %d attempts: %w", attempt, lastErr)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%w after %d attempts: %w", errUploadBudgetExhausted, attempt, lastErr)
		}

		wait := min(backoff, remaining)
		if onRetry != nil {
			onRetry(attempt, wait, lastErr)
		}

		select {
		case <-ctx.Done():
			return errors.Join(lastErr, context.Cause(ctx))
		case <-time.After(wait):
		}

		backoff = min(backoff*time.Duration(policy.multiplier), policy.maxBackoff)
	}
}

// isRetryableUploadErr classifies an upload failure. The default is RETRYABLE:
// a lost snapshot is unrecoverable and cascades to descendants, so a wasted
// retry is far cheaper than dropping a recoverable build. Only genuinely
// terminal conditions stop the loop.
func isRetryableUploadErr(err error) bool {
	switch {
	case errors.Is(err, build.NoDiffError{}):
		return false // nothing to upload
	case errors.Is(err, storage.ErrObjectNotExist):
		return false // source vanished; retry cannot recover it
	case errors.Is(err, context.Canceled):
		return false // parent cancelled (shutdown)
	default:
		// Includes per-attempt context.DeadlineExceeded, GCS 401/503, rate
		// limiting, and unknown errors — all worth retrying within the budget.
		return true
	}
}
