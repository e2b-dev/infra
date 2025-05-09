package proxy

import (
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/tracking"
)

const maxClientConns = 8192 // Reasonably big number that is lower than the number of available ports.
const idleTimeoutBufferUpstreamDownstream = 10

type Proxy struct {
	http.Server
	pool                      *pool.ProxyPool
	currentServerConnsCounter atomic.Int64
}

func New(
	port uint,
	poolSizePerConnectionKey int,
	idleTimeout time.Duration,
	getDestination func(r *http.Request) (*pool.Destination, error),
) *Proxy {
	p := pool.New(
		poolSizePerConnectionKey,
		maxClientConns,
		idleTimeout,
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
			Handler:           handler(p, getDestination),
		},
		pool: p,
	}
}

func (p *Proxy) TotalPoolConnections() uint64 {
	return p.pool.TotalConnections()
}

func (p *Proxy) CurrentServerConnections() int64 {
	return p.currentServerConnsCounter.Load()
}

func (p *Proxy) CurrentPoolSize() int {
	return p.pool.Size()
}

func (p *Proxy) CurrentPoolConnections() int64 {
	return p.pool.CurrentConnections()
}

func (p *Proxy) RemoveFromPool(connectionKey string) {
	p.pool.Close(connectionKey)
}

func (p *Proxy) ListenAndServe() error {
	l, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return err
	}

	return p.Serve(l)
}

func (p *Proxy) Serve(l net.Listener) error {
	return p.Server.Serve(tracking.NewListener(l, &p.currentServerConnsCounter))
}
