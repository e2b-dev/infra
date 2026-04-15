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
// Retries stop early when:
//   - fn succeeds (returns nil error)
//   - the parent ctx is cancelled/expired
//   - the error is not transient according to the GCS library
func retryWithBackoff(ctx context.Context, fn func() (int, error)) (int, error) {
	var (
		n       int
		err     error
		backoff = googleRetryInitialBackoff
	)

	for attempt := range googleMaxReadAttempts {
		n, err = fn()
		if err == nil || errors.Is(err, io.EOF) {
			return n, err
		}

		// Don't retry if the caller's context is done.
		if ctx.Err() != nil {
			break
		}

		// Don't retry errors the GCS library considers non-transient.
		if !storage.ShouldRetry(err) {
			break
		}

		// Don't sleep after the last attempt.
		if attempt < googleMaxReadAttempts-1 {
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return n, err
			}

			backoff = min(backoff*googleRetryBackoffMultiply, googleRetryMaxBackoff)
		}
	}

	return n, err
}
