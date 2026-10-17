package tracking

import (
	"io"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type halfCloser interface {
	CloseWrite() error
}

func tcpPair(t *testing.T) (net.Listener, net.Conn) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return listener, client
}

// net/http half-closes a server connection before closing it over an unread
// request body, so the client gets the end of the response instead of a reset.
func TestServerConnectionHalfCloses(t *testing.T) {
	t.Parallel()

	listener, client := tcpPair(t)
	var counter atomic.Int64
	server, err := NewListener(listener, &counter).Accept()
	require.NoError(t, err)
	assert.Equal(t, int64(1), counter.Load())

	cw, ok := server.(halfCloser)
	require.True(t, ok, "the server connection should offer net/http a half-close")
	require.NoError(t, cw.CloseWrite())

	_, err = client.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "the client should see the end of the server's output")

	_, err = client.Write([]byte("x"))
	require.NoError(t, err)
	buf := make([]byte, 1)
	n, err := server.Read(buf)
	require.NoError(t, err, "the server should still read after the half-close")
	assert.Equal(t, "x", string(buf[:n]))

	require.NoError(t, server.Close())
	assert.Equal(t, int64(0), counter.Load())
}

// A pool connection keeps a full close: half-closing an upgraded connection
// toward the sandbox would leave the proxy waiting for the sandbox to hang up.
func TestPoolConnectionDoesNotHalfClose(t *testing.T) {
	t.Parallel()

	_, client := tcpPair(t)
	var counter atomic.Int64
	conn := NewConnection(client, &counter, nil)

	_, ok := any(conn).(halfCloser)
	assert.False(t, ok)
}
