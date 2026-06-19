package netlimit

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// net.Pipe is synchronous and unbuffered: a write blocks until the peer reads,
// which is exactly the TCP-backpressure situation we want the deadline to bound.

func TestWriteTimeoutConn_TimesOutWhenPeerStalls(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close() // never read from client => server's write stalls
	defer server.Close()

	c := &writeTimeoutConn{Conn: server, timeout: 50 * time.Millisecond}

	start := time.Now()
	_, err := c.Write([]byte("hello"))

	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr, "expected a net.Error, got %v", err)
	require.True(t, netErr.Timeout(), "expected a timeout error, got %v", err)
	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
}

func TestWriteTimeoutConn_SucceedsWhenPeerReads(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	c := &writeTimeoutConn{Conn: server, timeout: time.Second}

	go func() {
		buf := make([]byte, 5)
		_, _ = io.ReadFull(client, buf)
	}()

	n, err := c.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
}

// The deadline must be reset before every write (idle write timeout), not set
// once for the whole connection. A peer that reads each message within the
// timeout keeps the stream alive even when the total span exceeds the timeout.
func TestWriteTimeoutConn_ResetsDeadlinePerWrite(t *testing.T) {
	t.Parallel()

	const (
		timeout   = 100 * time.Millisecond
		perWrite  = 40 * time.Millisecond // < timeout, so each write completes
		numWrites = 5                     // total span (~200ms) > timeout
	)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	c := &writeTimeoutConn{Conn: server, timeout: timeout}

	go func() {
		buf := make([]byte, 4)
		for range numWrites {
			time.Sleep(perWrite)
			if _, err := io.ReadFull(client, buf); err != nil {
				return
			}
		}
	}()

	for range numWrites {
		_, err := c.Write([]byte("ping"))
		require.NoError(t, err)
	}
}

func TestWriteTimeoutListener_WrapsAcceptedConn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	wln := WriteTimeoutListener(ln, time.Second)

	go func() {
		conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
		if dialErr == nil {
			defer conn.Close()
		}
	}()

	conn, err := wln.Accept()
	require.NoError(t, err)
	defer conn.Close()

	_, ok := conn.(*writeTimeoutConn)
	require.True(t, ok, "accepted conn should be wrapped")
}

func TestWriteTimeoutListener_NonPositiveTimeoutPassesThrough(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	wln := WriteTimeoutListener(ln, 0)
	require.Equal(t, ln, wln, "non-positive timeout should return the inner listener unchanged")

	go func() {
		conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
		if dialErr == nil {
			defer conn.Close()
		}
	}()

	conn, err := wln.Accept()
	require.NoError(t, err)
	defer conn.Close()

	_, ok := conn.(*writeTimeoutConn)
	require.False(t, ok, "accepted conn should not be wrapped when disabled")
}
