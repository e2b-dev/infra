//go:build linux

package server

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/retry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// defaultUploadRetryPolicy is the retry policy for pause-snapshot uploads:
// retry with a fresh per-attempt timeout under the total budget, with
// exponential backoff.
func defaultUploadRetryPolicy() retry.Policy {
	return retry.Policy{
		TotalBudget:    uploadTotalBudget,
		AttemptTimeout: uploadTimeout,
		InitialBackoff: uploadRetryInitialBackoff,
		MaxBackoff:     uploadRetryMaxBackoff,
		Multiplier:     uploadRetryBackoffMultiplier,
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
