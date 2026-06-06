package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
)

const (
	// defaultTunnelHost is dialed when the client omits the host query parameter.
	defaultTunnelHost = "localhost"

	// tunnelDialTimeout bounds how long we wait for the target to accept the connection
	// before failing the upgrade with a 502.
	tunnelDialTimeout = 10 * time.Second

	// tunnelKeepAliveInterval pings the WebSocket peer to keep the connection alive
	// through the upstream proxies' idle timeouts (orchestrator/client-proxy ~620s).
	tunnelKeepAliveInterval = 30 * time.Second
)

// GetTunnel upgrades the request to a WebSocket and relays raw TCP bytes between
// the client and a target (host:port) reachable inside the sandbox.
//
// The target is supplied as query parameters on the upgrade request
// (GET /tunnel?host=localhost&port=8080), so invalid parameters and dial failures
// are reported as ordinary HTTP responses before the upgrade rather than over a
// half-open WebSocket. After the upgrade the connection is a verbatim bidirectional
// relay: binary frames carry raw bytes with no framing or prefixes.
//
// Authentication is enforced by the WithAuthorization middleware (X-Access-Token
// header) before this handler runs.
func (a *API) GetTunnel(w http.ResponseWriter, r *http.Request, params GetTunnelParams) {
	operationID := logs.AssignOperationID()

	host := defaultTunnelHost
	if params.Host != nil && *params.Host != "" {
		host = *params.Host
	}

	if params.Port < 1 || params.Port > 65535 {
		a.logger.Error().
			Str(string(logs.OperationIDKey), operationID).
			Int("port", params.Port).
			Msg("tunnel request with invalid port")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("port must be between 1 and 65535, got %d", params.Port))

		return
	}

	target := net.JoinHostPort(host, strconv.Itoa(params.Port))

	// Dial the target before upgrading so failures surface as a normal HTTP status.
	dialer := net.Dialer{Timeout: tunnelDialTimeout}
	tcp, err := dialer.DialContext(r.Context(), "tcp", target)
	if err != nil {
		a.logger.Warn().
			Err(err).
			Str(string(logs.OperationIDKey), operationID).
			Str("target", target).
			Msg("tunnel dial failed")
		jsonError(w, http.StatusBadGateway, fmt.Errorf("failed to connect to %s: %w", target, err))

		return
	}
	defer tcp.Close()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow all origins, consistent with envd's allow-all CORS policy.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept already wrote the HTTP error response.
		a.logger.Error().
			Err(err).
			Str(string(logs.OperationIDKey), operationID).
			Str("target", target).
			Msg("tunnel websocket upgrade failed")

		return
	}
	defer conn.CloseNow()

	// Remove the default read limit; this is a raw relay and frames may be large.
	conn.SetReadLimit(-1)

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("target", target).
		Msg("tunnel established")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Keepalive pings keep the tunnel warm through the upstream proxies' idle timeouts.
	go a.tunnelKeepAlive(ctx, conn)

	// websocket.NetConn turns the WebSocket into a net.Conn so the relay is a pair of
	// io.Copy calls. MessageBinary ensures bytes pass through verbatim.
	wsConn := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	errc := make(chan error, 2)
	go func() { _, e := io.Copy(tcp, wsConn); errc <- e }() // client -> target
	go func() { _, e := io.Copy(wsConn, tcp); errc <- e }() // target -> client

	// Wait for either direction to finish, then tear down both so the other io.Copy unblocks.
	relayErr := <-errc
	cancel()
	tcp.Close()
	wsConn.Close()

	if relayErr != nil && !errors.Is(relayErr, io.EOF) && !errors.Is(relayErr, net.ErrClosed) && !errors.Is(relayErr, context.Canceled) {
		a.logger.Debug().
			Err(relayErr).
			Str(string(logs.OperationIDKey), operationID).
			Str("target", target).
			Msg("tunnel relay ended with error")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("target", target).
		Msg("tunnel closed")
}

func (a *API) tunnelKeepAlive(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(tunnelKeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}
