package netlimit

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubBody is an io.ReadCloser that counts reads and returns EOF, so the tests
// exercise the deadline logic rather than any real I/O.
type stubBody struct {
	reads int
}

func (b *stubBody) Read(_ []byte) (int, error) {
	b.reads++

	return 0, io.EOF
}

func (b *stubBody) Close() error { return nil }

func TestIdleReadCloser_SetsDeadlineBeforeEachRead(t *testing.T) {
	t.Parallel()

	var calls int
	set := func(time.Time) error {
		calls++

		return nil
	}

	body := &stubBody{}
	rc := IdleReadCloser(body, set, time.Second)

	for range 3 {
		_, _ = rc.Read(make([]byte, 1))
	}

	require.Equal(t, 3, calls, "deadline must be reset before every read")
	require.Equal(t, 3, body.reads)
}

func TestIdleReadCloser_NonPositiveTimeoutPassesThrough(t *testing.T) {
	t.Parallel()

	set := func(time.Time) error {
		t.Fatal("must not set deadline")

		return nil
	}

	body := &stubBody{}
	rc := IdleReadCloser(body, set, 0)

	require.Equal(t, body, rc, "non-positive timeout should return the body unchanged")
}

// When the underlying writer can't set deadlines, we must keep reading rather
// than fail an otherwise healthy upload — and stop calling setDeadline.
func TestIdleReadCloser_UnsupportedDeadlineKeepsReading(t *testing.T) {
	t.Parallel()

	var calls int
	set := func(time.Time) error {
		calls++

		return http.ErrNotSupported
	}

	body := &stubBody{}
	rc := IdleReadCloser(body, set, time.Second)

	for range 3 {
		_, err := rc.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
	}

	require.Equal(t, 1, calls, "should stop attempting after ErrNotSupported")
	require.Equal(t, 3, body.reads)
}

// A real (non-ErrNotSupported) deadline error must surface, not be swallowed.
func TestIdleReadCloser_RealDeadlineErrorSurfaces(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("conn closed")
	set := func(time.Time) error { return sentinel }

	body := &stubBody{}
	rc := IdleReadCloser(body, set, time.Second)

	_, err := rc.Read(make([]byte, 1))
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 0, body.reads, "read must not happen when deadline set fails")
}
