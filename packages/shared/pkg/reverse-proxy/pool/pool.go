package pool

import (
	"fmt"
	"log"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"go.uber.org/zap"
)

const hostConnectionSplit = 4

type ProxyPool struct {
	pool                *smap.Map[*client.ProxyClient]
	size                int
	maxClientConns      int
	idleTimeout         time.Duration
	totalConnsCounter   atomic.Uint64
	currentConnsCounter atomic.Int64
	clientLogger        *log.Logger
}

func New(size int, maxClientConns int, idleTimeout time.Duration) (*ProxyPool, error) {
	clientLogger, err := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)
	if err != nil {
		return nil, err
	}

	return &ProxyPool{
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

func (p *ProxyPool) keys(connectionKey string) []string {
	keys := make([]string, 0, p.size)

	for poolIdx := range p.size {
		keys = append(keys, getClientKey(connectionKey, poolIdx))
	}

	return keys
}

func (p *ProxyPool) Get(connectionKey string) *client.ProxyClient {
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

func (p *ProxyPool) Close(connectionKey string) {
	for _, key := range p.keys(connectionKey) {
		p.pool.RemoveCb(key, func(key string, proxy *client.ProxyClient, exists bool) bool {
			if proxy != nil {
				proxy.CloseIdleConnections()
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
