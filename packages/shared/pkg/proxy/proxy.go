package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/connlimit"
	"github.com/e2b-dev/infra/packages/shared/pkg/httpserver"
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
	idleTimeoutBufferUpstreamDownstream = 10 * time.Second
)

type Proxy struct {
	http.Server

	pool                      *pool.ProxyPool
	currentServerConnsCounter atomic.Int64
}

type options struct {
	upstreamTLS *tls.Config
}

type Option func(*options)

func WithUpstreamTLS(cfg *tls.Config) Option {
	return func(o *options) {
		o.upstreamTLS = cfg
	}
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
	opts ...Option,
) *Proxy {
	var cfg options
	for _, o := range opts {
		o(&cfg)
	}

	p := pool.New(
		maxClientConns,
		int(maxConnectionAttempts),
		idleTimeout,
		disableKeepAlives,
		cfg.upstreamTLS,
	)

	proxy := &Proxy{
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
	httpserver.ConfigureH2C(&proxy.Server)

	return proxy
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

func (p *Proxy) ListenAndServeTLS(ctx context.Context, certFile, keyFile string) error {
	return p.ListenAndServeTLSOn(ctx, p.Addr, certFile, keyFile)
}

func (p *Proxy) ListenAndServeTLSOn(ctx context.Context, addr, certFile, keyFile string) error {
	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return p.ServeTLS(l, certFile, keyFile)
}

func (p *Proxy) ServeTLS(l net.Listener, certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		l.Close()

		return fmt.Errorf("load proxy TLS cert: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}

	trackedListener := tracking.NewListener(l, &p.currentServerConnsCounter)
	tlsListener := tls.NewListener(trackedListener, tlsCfg)

	return p.Server.Serve(tlsListener)
}

func (p *Proxy) Serve(l net.Listener) error {
	return p.Server.Serve(tracking.NewListener(l, &p.currentServerConnsCounter))
}
