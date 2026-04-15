package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"cloud.google.com/go/storage"
)

// retryWithBackoff retries fn up to googleMaxReadAttempts times with
// exponential backoff. Each call to fn should use its own context.WithTimeout
// so that every attempt gets a full deadline.
//
// Returns the number of bytes read, the number of attempts made (1-indexed),
// and the final error. attempts == 1 means no retries occurred.
//
// Retries stop early when:
//   - fn succeeds (returns nil error)
//   - the parent ctx is cancelled/expired
//   - the error is not transient according to the GCS library
func retryWithBackoff(ctx context.Context, fn func() (int, error)) (int, int, error) {
	var (
		n       int
		err     error
		backoff = googleRetryInitialBackoff
	)

	for attempt := range googleMaxReadAttempts {
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
		if attempt < googleMaxReadAttempts-1 {
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return n, attempt + 1, err
			}

			backoff = min(backoff*googleRetryBackoffMultiply, googleRetryMaxBackoff)
		}
	}

	return n, googleMaxReadAttempts, err
}
