package proxy

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/client"
)

const maxClientConns = 8192 // Reasonably big number that is lower than the number of available ports.

type Proxy struct {
	http.Server
	pool                      *proxyPool
	currentServerConnsCounter *atomic.Int64
	noServerConns             *sync.Cond
}

func New(
	port uint,
	poolSize int,
	idleTimeout time.Duration,
	getProxyingInfo func(r *http.Request) (*client.ProxingInfo, error),
) (*Proxy, error) {
	pool, err := newProxyPool(
		poolSize,
		maxClientConns,
		idleTimeout,
	)
	if err != nil {
		return nil, err
	}

	var currentServerConnsCounter atomic.Int64
	noServerConns := sync.NewCond(&sync.Mutex{})

	return &Proxy{
		Server: http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			ReadTimeout:       0,
			WriteTimeout:      0,
			IdleTimeout:       idleTimeout,
			ReadHeaderTimeout: 0,
			Handler:           pool.handler(getProxyingInfo),
			ConnState: func(conn net.Conn, state http.ConnState) {
				if state == http.StateNew {
					currentServerConnsCounter.Add(1)
				} else if state == http.StateClosed {
					if currentServerConnsCounter.Add(-1) == 0 {
						noServerConns.Broadcast()
					}
				}
			},
		},
		currentServerConnsCounter: &currentServerConnsCounter,
		noServerConns:             noServerConns,
		pool:                      pool,
	}, nil
}

func (p *Proxy) TotalPoolConnections() uint64 {
	return p.pool.TotalConnections()
}

// WaitForNoServerConnections waits for all server connections (even the idle ones) to be closed.
func (p *Proxy) WaitForNoServerConnections() {
	for p.currentServerConnsCounter.Load() != 0 {
		p.noServerConns.L.Lock()
		defer p.noServerConns.L.Unlock()

		p.noServerConns.Wait()
	}
}

func (p *Proxy) CurrentServerConnections() int64 {
	return p.currentServerConnsCounter.Load()
}

func (p *Proxy) RemoveFromPool(connectionKey string) {
	p.pool.Close(connectionKey)
}
