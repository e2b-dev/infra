package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccessToken = "secret-access-token"

// newTunnelServer wires up an API guarded by the testAccessToken and serves the
// /tunnel route behind the same authorization middleware used in production.
func newTunnelServer(t *testing.T) *httptest.Server {
	t.Helper()

	logger := zerolog.Nop()
	api := &API{logger: &logger, accessToken: &SecureToken{}}
	require.NoError(t, api.accessToken.Set([]byte(testAccessToken)))

	m := chi.NewRouter()
	handler := HandlerFromMux(api, m)

	server := httptest.NewServer(api.WithAuthorization(handler))
	t.Cleanup(server.Close)

	return server
}

// startEchoServer starts a TCP server that echoes everything it receives and
// returns its port.
func startEchoServer(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	_, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)

	return port
}

func wsURL(serverURL, query string) string {
	return "ws://" + strings.TrimPrefix(serverURL, "http://") + "/tunnel?" + query
}

// dialTunnel opens the WebSocket tunnel and returns the connection (on success)
// and the HTTP handshake status code. It closes the response body itself so
// callers never have to.
func dialTunnel(ctx context.Context, t *testing.T, serverURL, query, token string) (*websocket.Conn, int, error) {
	t.Helper()

	opts := &websocket.DialOptions{}
	if token != "" {
		opts.HTTPHeader = http.Header{accessTokenHeader: []string{token}}
	}

	conn, resp, err := websocket.Dial(ctx, wsURL(serverURL, query), opts)

	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	return conn, status, err
}

func TestTunnelRelaysBytes(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)
	port := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialTunnel(ctx, t, server.URL, "host=127.0.0.1&port="+port, testAccessToken)
	require.NoError(t, err)
	defer conn.CloseNow()

	netConn := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	payload := []byte("hello raw tcp tunnel")
	_, err = netConn.Write(payload)
	require.NoError(t, err)

	got := make([]byte, len(payload))
	_, err = io.ReadFull(netConn, got)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestTunnelDefaultsHostToLocalhost(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)
	port := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// No host param -> defaults to localhost, which resolves to the echo server's listener.
	conn, _, err := dialTunnel(ctx, t, server.URL, "port="+port, testAccessToken)
	require.NoError(t, err)
	defer conn.CloseNow()

	netConn := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	_, err = netConn.Write([]byte("ping"))
	require.NoError(t, err)

	got := make([]byte, 4)
	_, err = io.ReadFull(netConn, got)
	require.NoError(t, err)
	assert.Equal(t, []byte("ping"), got)
}

func TestTunnelRejectsMissingToken(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)
	port := startEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, status, err := dialTunnel(ctx, t, server.URL, "host=127.0.0.1&port="+port, "")
	if conn != nil {
		conn.CloseNow()
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestTunnelRejectsInvalidPort(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, status, err := dialTunnel(ctx, t, server.URL, "host=127.0.0.1&port=not-a-number", testAccessToken)
	if conn != nil {
		conn.CloseNow()
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestTunnelRejectsOutOfRangePort(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, status, err := dialTunnel(ctx, t, server.URL, "host=127.0.0.1&port="+strconv.Itoa(70000), testAccessToken)
	if conn != nil {
		conn.CloseNow()
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestTunnelDialFailureReturnsBadGateway(t *testing.T) {
	t.Parallel()

	server := newTunnelServer(t)

	// Grab a port, then close the listener so the dial is refused.
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	require.NoError(t, l.Close())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, status, err := dialTunnel(ctx, t, server.URL, "host=127.0.0.1&port="+port, testAccessToken)
	if conn != nil {
		conn.CloseNow()
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusBadGateway, status)
}
