package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"cloud.google.com/go/storage"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// Fresh context.WithTimeout per attempt instead of the library's built-in
// retry which shared a single deadline across all attempts. Slow responses
// exhausted that shared deadline, leaving only 1-2 truncated attempts.
// Worst case: 3 × 10s + ~0.6s backoff ≈ 31s per range read.
const (
	googlePerAttemptTimeout    = 10 * time.Second
	googleMaxReadAttempts      = 3
	googleRetryInitialBackoff  = 200 * time.Millisecond
	googleRetryMaxBackoff      = 2 * time.Second
	googleRetryBackoffMultiply = 2
	minPerAttemptTimeout       = 1 * time.Second
)

// readRetryConfig holds resolved GCS range-read retry parameters.
type readRetryConfig struct {
	perAttemptTimeout time.Duration
	maxAttempts       int
}

type readRetryConfigKey struct{}

// WithReadRetryConfig resolves the GCS retry feature flags and stores the
// result in ctx. Call once per request (e.g. in CreateSandbox / ResumeSandbox);
// gcpObject.openRangeReader reads the resolved values via getReadRetryConfig.
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

// retryingRangeReader streams a bounded byte range from an upstream, re-opening
// the underlying reader on transient errors. The per-attempt timeout covers
// both the open RPC and all reads from the returned reader in that attempt.
//
// openFn is the transport seam: production wires it to a GCS NewRangeReader
// call; tests supply a scripted fake.
type retryingRangeReader struct {
	openFn            func(ctx context.Context, off, length int64) (io.ReadCloser, error)
	path              string          // used in error messages
	parentCtx         context.Context //nolint:containedctx // reader outlives openRangeReader call
	baseOff           int64
	totalLen          int64
	perAttemptTimeout time.Duration
	maxAttempts       int

	bytesRead     int64
	attemptsUsed  int
	backoff       time.Duration
	current       io.ReadCloser
	currentCancel context.CancelFunc
}

// openWithRetry opens a new underlying reader at baseOff+bytesRead, retrying
// transient open failures with exponential backoff until maxAttempts is
// reached. The returned reader carries its own context.WithTimeout so that
// subsequent reads share the per-attempt deadline — a stalled read trips the
// deadline, surfacing as a retryable error via Read() below.
func (r *retryingRangeReader) openWithRetry(ctx context.Context) error {
	var lastErr error
	for r.attemptsUsed < r.maxAttempts {
		if r.attemptsUsed > 0 {
			if sleepErr := sleepWithJitter(ctx, r.backoff); sleepErr != nil {
				lastErr = sleepErr

				break
			}
			r.backoff = min(r.backoff*googleRetryBackoffMultiply, googleRetryMaxBackoff)
		} else {
			r.backoff = googleRetryInitialBackoff
		}

		r.attemptsUsed++

		attemptCtx, cancel := context.WithTimeout(ctx, r.perAttemptTimeout)

		off := r.baseOff + r.bytesRead
		remaining := r.totalLen - r.bytesRead

		reader, err := r.openFn(attemptCtx, off, remaining)
		if err == nil {
			r.current = reader
			r.currentCancel = cancel

			return nil
		}

		cancel()
		lastErr = err

		if ctx.Err() != nil || !storage.ShouldRetry(err) {
			break
		}
	}

	return fmt.Errorf("failed to create GCS range reader for %q at %d+%d after %d attempts: %w", r.path, r.baseOff+r.bytesRead, r.totalLen-r.bytesRead, r.attemptsUsed, lastErr)
}

func (r *retryingRangeReader) Read(p []byte) (int, error) {
	if r.current == nil {
		return 0, io.ErrClosedPipe
	}

	remaining := r.totalLen - r.bytesRead
	if remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}

	for {
		n, err := r.current.Read(p)
		r.bytesRead += int64(n)

		if err == nil {
			return n, nil
		}

		if errors.Is(err, io.EOF) {
			if r.bytesRead >= r.totalLen {
				return n, io.EOF
			}
			// Short stream — treat like a transient mid-stream failure.
			err = io.ErrUnexpectedEOF
		}

		// Tear down the current attempt.
		_ = r.current.Close()
		r.currentCancel()
		r.current = nil

		if r.parentCtx.Err() != nil || !storage.ShouldRetry(err) || r.attemptsUsed >= r.maxAttempts {
			return n, fmt.Errorf("failed to read %q at %d (read %d/%d) after %d attempts: %w", r.path, r.baseOff+r.bytesRead, r.bytesRead, r.totalLen, r.attemptsUsed, err)
		}

		if openErr := r.openWithRetry(r.parentCtx); openErr != nil {
			return n, openErr
		}

		// Return partial bytes so io.ReadFull can keep advancing; its next Read
		// call will pull from the newly-opened reader.
		if n > 0 {
			return n, nil
		}

		remaining = r.totalLen - r.bytesRead
		if remaining <= 0 {
			return 0, io.EOF
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
}

func (r *retryingRangeReader) Close() error {
	if r.current == nil {
		return nil
	}

	err := r.current.Close()
	r.currentCancel()
	r.current = nil

	return err
}

// sleepWithJitter sleeps for d ± 25% or returns early if ctx is done.
func sleepWithJitter(ctx context.Context, d time.Duration) error {
	quarter := d / 4
	jittered := d - quarter + time.Duration(rand.Int64N(int64(quarter*2+1)))

	t := time.NewTimer(jittered)
	defer t.Stop()

	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
