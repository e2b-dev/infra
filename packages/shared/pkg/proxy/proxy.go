package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/connlimit"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/tracking"
)

// ConnectionLimitConfig bundles connection limiting and associated metric callbacks.
// When nil is passed, connection limiting is disabled.
type ConnectionLimitConfig struct {
	Limiter              *connlimit.ConnectionLimiter
	GetMaxLimit          func(ctx context.Context) int
	OnConnectionAcquired func(ctx context.Context, count int64)
	OnConnectionReleased func(ctx context.Context, durationMs int64)
	OnConnectionBlocked  func(ctx context.Context)
}

const (
	maxClientConns                      = 16384 // Reasonably big number that is lower than the number of available ports.
	idleTimeoutBufferUpstreamDownstream = 10
)

type Proxy struct {
	http.Server

	pool                      *pool.ProxyPool
	currentServerConnsCounter atomic.Int64
}

type MaxConnectionAttempts int

const (
	ClientProxyRetries  = 1
	SandboxProxyRetries = 5
)

func New(
	port uint16,
	maxConnectionAttempts MaxConnectionAttempts,
	idleTimeout time.Duration,
	getDestination func(r *http.Request) (*pool.Destination, error),
	connLimitConfig *ConnectionLimitConfig,
	disableKeepAlives bool,
) *Proxy {
	p := pool.New(
		maxClientConns,
		int(maxConnectionAttempts),
		idleTimeout,
		disableKeepAlives,
	)

	return &Proxy{
		Server: http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			ReadTimeout:  0,
			WriteTimeout: 0,
			// Downstream idle timeout (client facing) > upstream idle timeout (server facing)
			// otherwise there's a chance for a race condition when the server closes and the client tries to use the connection
			IdleTimeout:       idleTimeout + idleTimeoutBufferUpstreamDownstream,
			ReadHeaderTimeout: 0,
			Handler:           handler(p, getDestination, connLimitConfig),
		},
		pool: p,
	}
}

// TotalPoolConnections returns the total number of connections that have been established across whole pool.
func (p *Proxy) TotalPoolConnections() uint64 {
	return p.pool.TotalConnections()
}

// CurrentServerConnections returns the current number of connections that are alive across whole pool.
func (p *Proxy) CurrentServerConnections() int64 {
	return p.currentServerConnsCounter.Load()
}

func (p *Proxy) CurrentPoolSize() int {
	return p.pool.Size()
}

func (p *Proxy) CurrentPoolConnections() int64 {
	return p.pool.CurrentConnections()
}

func (p *Proxy) RemoveFromPool(connectionKey string) error {
	return p.pool.Close(connectionKey)
}

func (p *Proxy) ListenAndServe(ctx context.Context) error {
	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(ctx, "tcp", p.Addr)
	if err != nil {
		return err
	}

	return p.Serve(l)
}

func (p *Proxy) Serve(l net.Listener) error {
	return p.Server.Serve(tracking.NewListener(l, &p.currentServerConnsCounter))
}

// E2BClientIPHeader is an internal header set by client-proxy to forward
// the real client IP to the orchestrator proxy. It is always stripped
// before the request is forwarded to the sandbox.
const E2BClientIPHeader = "X-E2B-Client-IP"

// ExtractClientIP returns the real client IP from an HTTP request.
// Priority: X-E2B-Client-IP (internal, set by client-proxy) → X-Forwarded-For → RemoteAddr.
//
// GCP external HTTPS LB appends two entries to X-Forwarded-For:
//
//	X-Forwarded-For: <client-supplied>, <real-client-IP>, <GCP-LB-IP>
//
// We take the second-to-last entry, which is the real client IP added by
// the load balancer. Taking the first entry would be spoofable.

func ExtractClientIP(r *http.Request) string {
	if clientIP := r.Header.Get(E2BClientIPHeader); clientIP != "" {
		return clientIP
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")

		return strings.TrimSpace(parts[max(len(parts)-2, 0)])
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
