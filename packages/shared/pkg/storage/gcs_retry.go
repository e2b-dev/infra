package storage

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"time"

	"cloud.google.com/go/storage"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// readRetryConfig holds resolved GCS ReadAt retry parameters.
type readRetryConfig struct {
	perAttemptTimeout time.Duration
	maxAttempts       int
}

type readRetryConfigKey struct{}

// WithReadRetryConfig resolves the GCS retry feature flags and stores the
// result in ctx. Call once per request (e.g. in CreateSandbox / ResumeSandbox);
// gcpObject.ReadAt reads the resolved values via getReadRetryConfig.
func WithReadRetryConfig(ctx context.Context, flags featureFlagsClient) context.Context {
	cfg := readRetryConfig{
		perAttemptTimeout: time.Duration(flags.IntFlag(ctx, featureflags.GCSPerAttemptTimeoutMs)) * time.Millisecond,
		maxAttempts:       flags.IntFlag(ctx, featureflags.GCSMaxReadAttempts),
	}

	return context.WithValue(ctx, readRetryConfigKey{}, cfg)
}

// getReadRetryConfig returns the retry config from ctx, falling back to the
// package-level constants when no override is present.
func getReadRetryConfig(ctx context.Context) (perAttemptTimeout time.Duration, maxAttempts int) {
	if cfg, ok := ctx.Value(readRetryConfigKey{}).(readRetryConfig); ok {
		perAttemptTimeout = cfg.perAttemptTimeout
		maxAttempts = cfg.maxAttempts
	}

	if perAttemptTimeout <= 0 {
		perAttemptTimeout = googlePerAttemptTimeout
	}

	perAttemptTimeout = max(perAttemptTimeout, minPerAttemptTimeout)

	if maxAttempts <= 0 {
		maxAttempts = googleMaxReadAttempts
	}

	return perAttemptTimeout, maxAttempts
}

// retryWithBackoff retries fn up to maxAttempts times with exponential backoff.
// Each call to fn should use its own context.WithTimeout so that every attempt
// gets a full deadline.
//
// Returns the number of bytes read, the number of attempts made (1-indexed),
// and the final error. attempts == 1 means no retries occurred.
//
// Retries stop early when:
//   - fn succeeds (returns nil error)
//   - the parent ctx is cancelled/expired
//   - the error is not transient according to the GCS library
func retryWithBackoff(ctx context.Context, maxAttempts int, fn func() (int, error)) (int, int, error) {
	var (
		n       int
		err     error
		backoff = googleRetryInitialBackoff
	)

	for attempt := range maxAttempts {
		n, err = fn()
		if err == nil || errors.Is(err, io.EOF) {
			return n, attempt + 1, err
		}

		// Don't retry if the caller's context is done.
		if ctx.Err() != nil {
			return n, attempt + 1, err
		}

		// Don't retry errors the GCS library considers non-transient.
		if !storage.ShouldRetry(err) {
			return n, attempt + 1, err
		}

		// Don't sleep after the last attempt.
		if attempt < maxAttempts-1 {
			// Add ±25% jitter to spread out concurrent retries.
			quarter := backoff / 4
			jitteredBackoff := backoff - quarter + time.Duration(rand.Int64N(int64(quarter*2+1)))
			t := time.NewTimer(jitteredBackoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()

				return n, attempt + 1, ctx.Err()
			}

			backoff = min(backoff*googleRetryBackoffMultiply, googleRetryMaxBackoff)
		}
	}

	return n, maxAttempts, err
}
