package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPerAttemptTimeout = 100 * time.Millisecond

// scriptedOpen builds an openFn that returns responses in order. Each call
// advances the script by one entry. If more opens happen than entries, the
// test fails loudly.
type openResponse struct {
	reader io.ReadCloser
	err    error
}

func scriptedOpen(t *testing.T, responses ...openResponse) (func(ctx context.Context, off, length int64) (io.ReadCloser, error), *int) {
	t.Helper()

	var calls int

	return func(_ context.Context, _, _ int64) (io.ReadCloser, error) {
		if calls >= len(responses) {
			t.Fatalf("openFn called %d times, script only has %d entries", calls+1, len(responses))
		}
		r := responses[calls]
		calls++

		return r.reader, r.err
	}, &calls
}

// stepReader delivers bytes in sized steps and optionally returns an error
// after delivering a configured number of bytes.
type stepReader struct {
	data       []byte
	pos        int
	stepSize   int // 0 means "hand over whatever the caller asks for"
	failAfter  int // 0 means "don't inject failure"
	failErr    error
	closed     bool
	closeCalls int
}

func (r *stepReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	// When failAfter is set, cap the effective data boundary at failAfter so
	// we can't accidentally deliver past it and miss the injection point.
	end := len(r.data)
	if r.failAfter > 0 && r.failAfter < end {
		end = r.failAfter
	}

	want := len(p)
	if r.stepSize > 0 && want > r.stepSize {
		want = r.stepSize
	}
	if rem := end - r.pos; want > rem {
		want = rem
	}

	n := copy(p[:want], r.data[r.pos:r.pos+want])
	r.pos += n

	if r.failAfter > 0 && r.pos >= r.failAfter {
		return n, r.failErr
	}

	if r.pos >= len(r.data) {
		return n, io.EOF
	}

	return n, nil
}

func (r *stepReader) Close() error {
	r.closeCalls++
	r.closed = true

	return nil
}

// newRetryingReader builds a retryingRangeReader wired to the given openFn.
// Uses testPerAttemptTimeout so reads don't actually wait on real deadlines.
// Tests that want fast retries should also set r.backoff = time.Microsecond
// after construction.
func newRetryingReader(
	ctx context.Context,
	openFn func(context.Context, int64, int64) (io.ReadCloser, error),
	off, length int64,
	maxAttempts int,
) *retryingRangeReader {
	return &retryingRangeReader{
		openFn:            openFn,
		path:              "test/obj",
		parentCtx:         ctx,
		baseOff:           off,
		totalLen:          length,
		perAttemptTimeout: testPerAttemptTimeout,
		maxAttempts:       maxAttempts,
	}
}

// readAll drains a reader into a byte slice, treating io.EOF as success.
func readAll(t *testing.T, r io.Reader) ([]byte, error) {
	t.Helper()
	var buf []byte
	tmp := make([]byte, 64)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}

func TestRetryingRangeReader_OpenSucceedsFirstTry(t *testing.T) {
	t.Parallel()
	payload := []byte("hello, world")
	reader := &stepReader{data: append([]byte(nil), payload...)}
	openFn, calls := scriptedOpen(t, openResponse{reader: reader})

	r := newRetryingReader(t.Context(), openFn, 0, int64(len(payload)), 3)
	require.NoError(t, r.openWithRetry(t.Context()))

	got, err := readAll(t, r)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equal(t, 1, *calls, "no retries should happen on success")
	require.NoError(t, r.Close())
	assert.Equal(t, 1, reader.closeCalls)
}

func TestRetryingRangeReader_OpenRetriesTransient(t *testing.T) {
	t.Parallel()
	payload := []byte("retry-then-ok")
	good := &stepReader{data: append([]byte(nil), payload...)}
	openFn, calls := scriptedOpen(t,
		openResponse{err: transientErr()},
		openResponse{err: transientErr()},
		openResponse{reader: good},
	)

	// Shrink backoff so the test is quick.
	r := newRetryingReader(t.Context(), openFn, 0, int64(len(payload)), 3)
	r.backoff = time.Microsecond

	require.NoError(t, r.openWithRetry(t.Context()))

	got, err := readAll(t, r)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equal(t, 3, *calls, "openFn should be called exactly 3 times")
	assert.Equal(t, 3, r.attemptsUsed)
	require.NoError(t, r.Close())
}

func TestRetryingRangeReader_OpenExhaustsAttempts(t *testing.T) {
	t.Parallel()
	openFn, calls := scriptedOpen(t,
		openResponse{err: transientErr()},
		openResponse{err: transientErr()},
		openResponse{err: transientErr()},
	)

	r := newRetryingReader(t.Context(), openFn, 0, 100, 3)
	r.backoff = time.Microsecond

	err := r.openWithRetry(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.Equal(t, 3, *calls)
	assert.Nil(t, r.current, "no current reader when all attempts fail")
}

func TestRetryingRangeReader_OpenNonTransientNotRetried(t *testing.T) {
	t.Parallel()
	openFn, calls := scriptedOpen(t, openResponse{err: permanentErr()})

	r := newRetryingReader(t.Context(), openFn, 0, 100, 5)
	r.backoff = time.Microsecond

	err := r.openWithRetry(t.Context())
	require.Error(t, err)
	assert.Equal(t, 1, *calls, "permanent error should not be retried")
	assert.Equal(t, 1, r.attemptsUsed)
}

func TestRetryingRangeReader_ResumesAfterMidStreamError(t *testing.T) {
	t.Parallel()
	// Non-zero baseOff mirrors a real chunk fetch so we also exercise the
	// wrapper's offset arithmetic on retry (second open must be at baseOff+6).
	const baseOff int64 = 1024
	payload := []byte("0123456789ABCDEF") // 16 bytes
	// First reader delivers 6 bytes, then a transient mid-stream error.
	first := &stepReader{
		data:      append([]byte(nil), payload...),
		failAfter: 6,
		failErr:   transientErr(),
	}
	// Second reader delivers the remaining 10 bytes.
	second := &stepReader{data: append([]byte(nil), payload[6:]...)}

	var reopenOff, reopenLen int64
	reopenCalls := 0
	openFn := func(_ context.Context, off, length int64) (io.ReadCloser, error) {
		reopenCalls++
		if reopenCalls == 1 {
			return first, nil
		}
		reopenOff, reopenLen = off, length

		return second, nil
	}

	r := newRetryingReader(t.Context(), openFn, baseOff, int64(len(payload)), 3)
	r.backoff = time.Microsecond

	require.NoError(t, r.openWithRetry(t.Context()))

	got, err := readAll(t, r)
	require.NoError(t, err)
	assert.Equal(t, payload, got, "caller receives the full range despite mid-stream error")
	assert.Equal(t, 2, reopenCalls)
	assert.Equal(t, 1, first.closeCalls, "first reader should be closed when retry kicks in")
	assert.Equal(t, baseOff+6, reopenOff, "reopen must target baseOff + bytesRead")
	assert.Equal(t, int64(10), reopenLen, "reopen must request the remaining bytes")
}

func TestRetryingRangeReader_MidStreamNonTransientSurfaces(t *testing.T) {
	t.Parallel()
	payload := []byte("0123456789")
	bad := &stepReader{
		data:      append([]byte(nil), payload...),
		failAfter: 4,
		failErr:   permanentErr(),
	}
	openFn, calls := scriptedOpen(t, openResponse{reader: bad})

	r := newRetryingReader(t.Context(), openFn, 0, int64(len(payload)), 5)
	r.backoff = time.Microsecond

	require.NoError(t, r.openWithRetry(t.Context()))
	_, err := readAll(t, r)
	require.ErrorIs(t, err, bad.failErr, "non-transient error should surface verbatim")
	assert.Equal(t, 1, *calls, "no reopen on non-transient mid-stream failure")
}

func TestRetryingRangeReader_ShortStreamIsTransient(t *testing.T) {
	t.Parallel()
	payload := []byte("complete-payload") // 16 bytes
	// First reader delivers 7 bytes and then EOF — short stream (totalLen=16).
	truncated := &stepReader{data: append([]byte(nil), payload[:7]...)}
	// Retry supplies the remaining 9 bytes at off=7, length=9.
	rest := &stepReader{data: append([]byte(nil), payload[7:]...)}

	openFn, calls := scriptedOpen(t,
		openResponse{reader: truncated},
		openResponse{reader: rest},
	)

	r := newRetryingReader(t.Context(), openFn, 0, int64(len(payload)), 3)
	r.backoff = time.Microsecond

	require.NoError(t, r.openWithRetry(t.Context()))
	got, err := readAll(t, r)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equal(t, 2, *calls)
}

func TestRetryingRangeReader_ParentCtxCancelStopsRetry(t *testing.T) {
	t.Parallel()
	// openFn always returns transient; test cancels parent ctx after the first
	// failure to prove the retry loop exits via ctx, not via budget.
	var openFn func(context.Context, int64, int64) (io.ReadCloser, error)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()

	calls := 0
	openFn = func(_ context.Context, _, _ int64) (io.ReadCloser, error) {
		calls++
		if calls == 1 {
			cancel() // parent is cancelled mid-retry
		}

		return nil, transientErr()
	}

	r := newRetryingReader(parent, openFn, 0, 100, 5)
	r.backoff = 10 * time.Millisecond

	err := r.openWithRetry(parent)
	require.Error(t, err)
	assert.LessOrEqual(t, calls, 2, "loop should exit quickly after parent cancel; got %d calls", calls)
	assert.Less(t, r.attemptsUsed, 5, "budget should not be exhausted when ctx cancels first")
}

func TestRetryingRangeReader_CloseCancelsAndClosesUnderlying(t *testing.T) {
	t.Parallel()
	reader := &stepReader{data: bytes.Repeat([]byte{0xAB}, 32)}
	openFn, _ := scriptedOpen(t, openResponse{reader: reader})

	r := newRetryingReader(t.Context(), openFn, 0, 32, 3)
	require.NoError(t, r.openWithRetry(t.Context()))

	// Capture the per-attempt ctx cancel by exercising Close().
	require.NoError(t, r.Close())
	assert.Equal(t, 1, reader.closeCalls, "underlying reader closed exactly once")
	assert.Nil(t, r.current, "current reader cleared after Close")

	// Second Close is a no-op.
	require.NoError(t, r.Close())
	assert.Equal(t, 1, reader.closeCalls, "Close is idempotent")
}

// Sanity check that our transient/permanent error helpers actually classify
// the way we expect — otherwise the tests above would silently mis-exercise
// the retry logic.
func TestTransientAndPermanentHelpersClassifyCorrectly(t *testing.T) {
	t.Parallel()
	assert.True(t, storage.ShouldRetry(transientErr()), "transientErr must be retryable")
	assert.False(t, storage.ShouldRetry(permanentErr()), "permanentErr must not be retryable")
	assert.NotErrorIs(t, transientErr(), permanentErr())
}
