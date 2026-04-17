package storage

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// transientErr returns a gRPC UNAVAILABLE error that storage.ShouldRetry
// considers retryable.
func transientErr() error {
	return status.Error(codes.Unavailable, "transient")
}

// permanentErr returns an error that storage.ShouldRetry does NOT retry.
func permanentErr() error {
	return fmt.Errorf("permanent: object not found")
}

func TestRetryWithBackoff_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()

	calls := 0

	n, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++

		return 42, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 42, n)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, attempts)
}

func TestRetryWithBackoff_SuccessAfterTransientFailures(t *testing.T) {
	t.Parallel()

	calls := 0

	n, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, transientErr()
		}

		return 100, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, 3, calls)
	assert.Equal(t, 3, attempts)
}

func TestRetryWithBackoff_ExhaustsAllAttempts(t *testing.T) {
	t.Parallel()

	calls := 0

	_, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++

		return 0, transientErr()
	})

	require.Error(t, err)
	assert.Equal(t, googleMaxReadAttempts, calls)
	assert.Equal(t, googleMaxReadAttempts, attempts)
}

func TestRetryWithBackoff_StopsOnPermanentError(t *testing.T) {
	t.Parallel()

	calls := 0

	_, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++

		return 0, permanentErr()
	})

	require.Error(t, err)
	assert.Equal(t, 1, calls, "should not retry permanent errors")
	assert.Equal(t, 1, attempts)
}

func TestRetryWithBackoff_StopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	calls := 0

	_, attempts, err := retryWithBackoff(ctx, googleMaxReadAttempts, func() (int, error) {
		calls++
		if calls == 2 {
			cancel()
		}

		return 0, transientErr()
	})

	require.Error(t, err)
	assert.Equal(t, 2, calls, "should stop after context is cancelled")
	assert.Equal(t, 2, attempts)
}

func TestRetryWithBackoff_RespectsDeadlineDuringBackoff(t *testing.T) {
	t.Parallel()

	// Context that expires well before all retries can complete.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	calls := 0

	_, attempts, err := retryWithBackoff(ctx, googleMaxReadAttempts, func() (int, error) {
		calls++

		return 0, transientErr()
	})

	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "should return context error, not stale GCS error")
	// Should not have run all attempts — the context deadline should have
	// cut the backoff sleep short.
	assert.Less(t, calls, googleMaxReadAttempts)
	assert.Equal(t, calls, attempts)
	assert.Less(t, elapsed, 5*time.Second, "should not wait for full backoff sequence")
}

func TestRetryWithBackoff_BackoffIncreases(t *testing.T) {
	t.Parallel()

	attempts := make([]time.Time, 0, googleMaxReadAttempts)

	_, _, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		attempts = append(attempts, time.Now())

		return 0, transientErr()
	})

	require.Error(t, err)
	require.Len(t, attempts, googleMaxReadAttempts)

	// Verify that gaps between attempts increase (exponential backoff).
	// First gap should be ~200ms, second ~400ms, etc. Allow generous margins.
	for i := 2; i < len(attempts); i++ {
		prevGap := attempts[i-1].Sub(attempts[i-2])
		thisGap := attempts[i].Sub(attempts[i-1])

		// Each gap should be at least 1.5x the previous (2x minus some scheduling jitter).
		assert.Greaterf(t, thisGap, prevGap*3/4,
			"gap %d (%v) should be roughly larger than gap %d (%v)",
			i-1, thisGap, i-2, prevGap)
	}
}

func TestRetryWithBackoff_PreservesLastNValue(t *testing.T) {
	t.Parallel()

	calls := 0

	n, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++

		// Return partial read count even on error.
		return calls * 10, transientErr()
	})

	require.Error(t, err)
	// n should reflect the value from the last attempt.
	assert.Equal(t, googleMaxReadAttempts*10, n)
	assert.Equal(t, googleMaxReadAttempts, attempts)
}

func TestRetryWithBackoff_EOFTreatedAsSuccess(t *testing.T) {
	t.Parallel()

	calls := 0

	// io.EOF signals a successful short read (last chunk of a file).
	// retryWithBackoff must not retry it.
	n, attempts, err := retryWithBackoff(t.Context(), googleMaxReadAttempts, func() (int, error) {
		calls++

		return 512, io.EOF
	})

	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, 512, n)
	assert.Equal(t, 1, calls, "should not retry io.EOF")
	assert.Equal(t, 1, attempts)
}

// stubFlags is a minimal featureFlagsClient for testing. It returns the
// configured values for the two GCS retry flags and zero for everything else.
type stubFlags struct {
	perAttemptTimeoutMs int
	maxReadAttempts     int
}

func (s *stubFlags) BoolFlag(_ context.Context, _ featureflags.BoolFlag, _ ...ldcontext.Context) bool {
	return false
}

func (s *stubFlags) IntFlag(_ context.Context, flag featureflags.IntFlag, _ ...ldcontext.Context) int {
	switch flag {
	case featureflags.GCSPerAttemptTimeoutMs:
		return s.perAttemptTimeoutMs
	case featureflags.GCSMaxReadAttempts:
		return s.maxReadAttempts
	default:
		return 0
	}
}

func TestGetReadRetryConfig_Defaults(t *testing.T) {
	t.Parallel()

	timeout, maxAttempts := getReadRetryConfig(t.Context())

	assert.Equal(t, googlePerAttemptTimeout, timeout)
	assert.Equal(t, googleMaxReadAttempts, maxAttempts)
}

func TestGetReadRetryConfig_FlagsOverride(t *testing.T) {
	t.Parallel()

	ctx := WithReadRetryConfig(t.Context(), &stubFlags{
		perAttemptTimeoutMs: 42000,
		maxReadAttempts:     7,
	})

	timeout, maxAttempts := getReadRetryConfig(ctx)

	assert.Equal(t, 42*time.Second, timeout)
	assert.Equal(t, 7, maxAttempts)
}

func TestGetReadRetryConfig_PartialOverride(t *testing.T) {
	t.Parallel()

	// Only set MaxAttempts, leave PerAttemptTimeoutMs zero → should fall back to default.
	ctx := WithReadRetryConfig(t.Context(), &stubFlags{
		maxReadAttempts: 5,
	})

	timeout, maxAttempts := getReadRetryConfig(ctx)

	assert.Equal(t, googlePerAttemptTimeout, timeout, "zero timeout should fall back to default")
	assert.Equal(t, 5, maxAttempts)
}

func TestGetReadRetryConfig_TooSmallTimeoutClampedToMin(t *testing.T) {
	t.Parallel()

	ctx := WithReadRetryConfig(t.Context(), &stubFlags{
		perAttemptTimeoutMs: 1, // 1ms — below minPerAttemptTimeout
		maxReadAttempts:     2,
	})

	timeout, maxAttempts := getReadRetryConfig(ctx)

	assert.Equal(t, minPerAttemptTimeout, timeout, "timeout below minimum should be clamped")
	assert.Equal(t, 2, maxAttempts)
}

func TestRetryWithBackoff_RespectsCustomMaxAttempts(t *testing.T) {
	t.Parallel()

	calls := 0

	_, attempts, err := retryWithBackoff(t.Context(), 5, func() (int, error) {
		calls++

		return 0, transientErr()
	})

	require.Error(t, err)
	assert.Equal(t, 5, calls)
	assert.Equal(t, 5, attempts)
}
