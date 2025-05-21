package pool

import (
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// hostConnectionSplit is used for splitting the total number of connections between the hosts.
// This is used to limit the number of connections per host to avoid exhausting the number of available via one host.
// The total number of connection per host will be total connections / hostConnectionSplit.
// If the total connections is lower than hostConnectionSplit, the total connections will be used for each host.
const hostConnectionSplit = 4

type ProxyPool struct {
	pool                 *smap.Map[*proxyClient]
	sizePerConnectionKey int
	maxClientConns       int
	idleTimeout          time.Duration
	totalConnsCounter    atomic.Uint64
	currentConnsCounter  atomic.Int64
}

func New(maxClientConns int, idleTimeout time.Duration) *ProxyPool {
	return &ProxyPool{
		pool:           smap.New[*proxyClient](),
		maxClientConns: maxClientConns,
		idleTimeout:    idleTimeout,
	}
}

func (p *ProxyPool) Get(d *Destination) *proxyClient {
	return p.pool.Upsert(d.ConnectionKey, nil, func(exist bool, inMapValue *proxyClient, newValue *proxyClient) *proxyClient {
		if exist && inMapValue != nil {
			return inMapValue
		}

		withFields := make([]zap.Field, 0)
		if d.IncludeSandboxIdInProxyErrorLogger {
			withFields = append(withFields, zap.String("sandbox_id", d.SandboxId))
		}

		logger, err := zap.NewStdLogAt(zap.L().With(withFields...), zap.ErrorLevel)
		if err != nil {
			zap.L().Warn("failed to create logger", zap.Error(err))
		}

		return newProxyClient(
			p.maxClientConns,
			// We limit the max number of connections per host to avoid exhausting the number of available via one host.
			func() int {
				if p.maxClientConns <= hostConnectionSplit {
					return p.maxClientConns
				}

				return p.maxClientConns / hostConnectionSplit
			}(),
			p.idleTimeout,
			&p.totalConnsCounter,
			&p.currentConnsCounter,
			logger,
		)
	})
}

func (p *ProxyPool) Close(connectionKey string) {
	p.pool.RemoveCb(connectionKey, func(key string, proxy *proxyClient, exists bool) bool {
		if proxy != nil {
			proxy.closeIdleConnections()
		}

		return true
	})
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
