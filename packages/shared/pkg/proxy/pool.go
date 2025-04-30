package proxy

import (
	"fmt"
	"log"
	"math/rand"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// hostConnectionSplit is the number of connections per host.
// This is used to limit the number of connections per host to avoid exhausting the number of available via one host.
// The total number of connection per host will be total connections / hostConnectionSplit.
// If the total connections is lower than hostConnectionSplit, the total connections will be used for each host.
const hostConnectionSplit = 4

type proxyPool struct {
	pool                *smap.Map[*client.ProxyClient]
	size                int
	maxClientConns      int
	idleTimeout         time.Duration
	totalConnsCounter   atomic.Uint64
	currentConnsCounter atomic.Int64
	clientLogger        *log.Logger
}

func newProxyPool(size int, maxClientConns int, idleTimeout time.Duration) (*proxyPool, error) {
	clientLogger, err := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)
	if err != nil {
		return nil, err
	}

	return &proxyPool{
		pool:           smap.New[*client.ProxyClient](),
		size:           size,
		maxClientConns: maxClientConns,
		idleTimeout:    idleTimeout,
		clientLogger:   clientLogger,
	}, nil
}

func getClientKey(connectionKey string, poolIdx int) string {
	return fmt.Sprintf("%s:%d", connectionKey, poolIdx)
}

func (p *proxyPool) keys(connectionKey string) []string {
	keys := make([]string, 0, p.size)

	for poolIdx := range p.size {
		keys = append(keys, getClientKey(connectionKey, poolIdx))
	}

	return keys
}

func (p *proxyPool) Get(connectionKey string) *client.ProxyClient {
	randomIdx := rand.Intn(p.size)

	key := getClientKey(connectionKey, randomIdx)

	return p.pool.Upsert(key, nil, func(exist bool, inMapValue *client.ProxyClient, newValue *client.ProxyClient) *client.ProxyClient {
		if exist && inMapValue != nil {
			return inMapValue
		}

		return client.NewProxyClient(
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

func (p *proxyPool) Close(connectionKey string) {
	for _, key := range p.keys(connectionKey) {
		p.pool.RemoveCb(key, func(key string, proxy *client.ProxyClient, exists bool) bool {
			if proxy != nil {
				proxy.CloseIdleConnections()
			}

			return true
		})
	}
}

func (p *proxyPool) TotalConnections() uint64 {
	return p.totalConnsCounter.Load()
}

func (p *proxyPool) CurrentConnections() int64 {
	return p.currentConnsCounter.Load()
}
