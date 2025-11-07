package pool

import (
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// hostConnectionSplit is used for splitting the total number of connections between the hosts.
// This is used to limit the number of connections per host to avoid exhausting the number of available via one host.
// The total number of connection per host will be total connections / hostConnectionSplit.
// If the total connections is lower than hostConnectionSplit, the total connections will be used for each host.
const hostConnectionSplit = 4

type ProxyPool struct {
	pool                  *smap.Map[*ProxyClient]
	maxClientConns        int
	maxConnectionAttempts int
	idleTimeout           time.Duration
	totalConnsCounter     atomic.Uint64
	currentConnsCounter   atomic.Int64
}

func New(maxClientConns, maxConnectionAttempts int, idleTimeout time.Duration) *ProxyPool {
	return &ProxyPool{
		pool:                  smap.New[*ProxyClient](),
		maxClientConns:        maxClientConns,
		maxConnectionAttempts: maxConnectionAttempts,
		idleTimeout:           idleTimeout,
	}
}

func (p *ProxyPool) Get(d *Destination) *ProxyClient {
	return p.pool.Upsert(d.ConnectionKey, nil, func(exist bool, inMapValue *ProxyClient, _ *ProxyClient) *ProxyClient {
		if !exist || inMapValue == nil {
			inMapValue = newProxyClient(
				p.maxClientConns,
				// We limit the max number of connections per host to avoid exhausting the number of available via one host.
				func() int {
					if p.maxClientConns <= hostConnectionSplit {
						return p.maxClientConns
					}

					return p.maxClientConns / hostConnectionSplit
				}(),
				p.maxConnectionAttempts,
				p.idleTimeout,
				&p.totalConnsCounter,
				&p.currentConnsCounter,
			)
		}

		return inMapValue.WithDestination(d)
	})
}

func (p *ProxyPool) Close(connectionKey string) (err error) {
	p.pool.RemoveCb(connectionKey, func(_ string, proxy *ProxyClient, _ bool) bool {
		if proxy != nil {
			proxy.closeIdleConnections()
			err = proxy.resetAllConnections()
		}

		return true
	})

	return err
}

func (p *ProxyPool) TotalConnections() uint64 {
	return p.totalConnsCounter.Load()
}

func (p *ProxyPool) CurrentConnections() int64 {
	return p.currentConnsCounter.Load()
}

func (p *ProxyPool) Size() int {
	return p.pool.Count()
}
