package reverse_proxy

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	pool "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/routing"
)

const MaxTotalIdleConnections = 8192 // Reasonably big number that is lower than the number of available ports.

type Proxy struct {
	http.Server
	currentDownstreamConnections *atomic.Int64
	noDownstreamConnections      *sync.Cond
	pool                         *pool.ProxyPool
}

func New(
	port uint,
	idleTimeout time.Duration,
	poolSizePerConnectionKey int,
	connectionTimeout time.Duration,
	maxConnectionDuration time.Duration,
	getRoutingTarget func(r *http.Request) (*client.RoutingTarget, error),
) *Proxy {
	pool := pool.NewProxyPool(
		maxConnectionDuration,
		poolSizePerConnectionKey,
		MaxTotalIdleConnections,
		idleTimeout,
		connectionTimeout,
	)

	var downstreamConnections atomic.Int64
	noDownstreamConnections := sync.NewCond(&sync.Mutex{})

	return &Proxy{
		Server: http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			ReadTimeout:       maxConnectionDuration,
			WriteTimeout:      maxConnectionDuration,
			IdleTimeout:       idleTimeout,
			ReadHeaderTimeout: 20 * time.Second,
			Handler:           http.HandlerFunc(routing.Handle(pool, getRoutingTarget)),
			ConnState: func(conn net.Conn, state http.ConnState) {
				if state == http.StateNew {
					downstreamConnections.Add(1)
				} else if state == http.StateClosed {
					if downstreamConnections.Add(-1) == 0 {
						noDownstreamConnections.Broadcast()
					}
				}
			},
		},
		currentDownstreamConnections: &downstreamConnections,
		noDownstreamConnections:      noDownstreamConnections,
		pool:                         pool,
	}
}

func (p *Proxy) TotalUpstreamConnections() uint64 {
	return p.pool.TotalEstablishedConnections()
}

// WaitForNoDownstreamConnections waits for all downstream connections (even the idle ones) to be closed.
func (p *Proxy) WaitForNoDownstreamConnections() {
	for p.currentDownstreamConnections.Load() != 0 {
		p.noDownstreamConnections.L.Lock()
		defer p.noDownstreamConnections.L.Unlock()

		p.noDownstreamConnections.Wait()
	}
}

func (p *Proxy) CurrentDownstreamConnections() int64 {
	return p.currentDownstreamConnections.Load()
}

func (p *Proxy) RemoveFromPool(connectionKey string) {
	p.pool.Close(connectionKey)
}
