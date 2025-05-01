package pool

import (
	"fmt"
	"log"
	"math/rand"
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
	clientLogger         *log.Logger
}

func New(sizePerConnectionKey, maxClientConns int, idleTimeout time.Duration) (*ProxyPool, error) {
	clientLogger, err := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)
	if err != nil {
		return nil, err
	}

	return &ProxyPool{
		pool:                 smap.New[*proxyClient](),
		sizePerConnectionKey: sizePerConnectionKey,
		maxClientConns:       maxClientConns,
		idleTimeout:          idleTimeout,
		clientLogger:         clientLogger,
	}, nil
}

func getClientKey(connectionKey string, poolIdx int) string {
	return fmt.Sprintf("%s:%d", connectionKey, poolIdx)
}

func (p *ProxyPool) keys(connectionKey string) []string {
	keys := make([]string, 0, p.sizePerConnectionKey)

	for poolIdx := range p.sizePerConnectionKey {
		keys = append(keys, getClientKey(connectionKey, poolIdx))
	}

	return keys
}

func (p *ProxyPool) Get(connectionKey string) *proxyClient {
	randomIdx := rand.Intn(p.sizePerConnectionKey)

	key := getClientKey(connectionKey, randomIdx)

	return p.pool.Upsert(key, nil, func(exist bool, inMapValue *proxyClient, newValue *proxyClient) *proxyClient {
		if exist && inMapValue != nil {
			return inMapValue
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
			p.clientLogger,
		)
	})
}

func (p *ProxyPool) Close(connectionKey string) {
	for _, key := range p.keys(connectionKey) {
		p.pool.RemoveCb(key, func(key string, proxy *proxyClient, exists bool) bool {
			if proxy != nil {
				proxy.closeIdleConnections()
			}

			return true
		})
	}
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
