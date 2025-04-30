package pool

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	hostConnectionSplit = 4
)

type ProxyPool struct {
	pool                        *expirable.LRU[string, *httputil.ReverseProxy]
	mu                          sync.Mutex
	poolSize                    int
	connectionLimitPerProxy     int
	totalEstablishedConnections atomic.Uint64
	clientIdleTimeout           time.Duration
	clientConnectionTimeout     time.Duration
}

func NewProxyPool(
	maxDuration time.Duration,
	poolSize int,
	connectionLimitPerProxy int,
	clientIdleTimeout,
	clientConnectionTimeout time.Duration,
) *ProxyPool {
	return &ProxyPool{
		pool: expirable.NewLRU(0, func(key string, value *httputil.ReverseProxy) {
			if value != nil {
				value.Transport.(*http.Transport).CloseIdleConnections()
			}
		}, maxDuration),
		poolSize:                poolSize,
		connectionLimitPerProxy: connectionLimitPerProxy,
		clientIdleTimeout:       clientIdleTimeout,
		clientConnectionTimeout: clientConnectionTimeout,
	}
}

func (p *ProxyPool) createProxyClient() (*httputil.ReverseProxy, error) {
	proxyClient, err := client.NewProxyClient(
		p.clientIdleTimeout,
		p.clientConnectionTimeout,
		p.poolSize,
		// We limit the max number of connections per host to avoid exhausting the number of available via one host.
		func() int {
			if p.connectionLimitPerProxy <= hostConnectionSplit {
				return p.connectionLimitPerProxy
			}

			return p.connectionLimitPerProxy / hostConnectionSplit
		}(),
	)
	if err != nil {
		return nil, err
	}

	return &proxyClient.ReverseProxy, nil
}

func getLRUKey(connectionKey string, poolIdx int) string {
	return fmt.Sprintf("%s:%d", connectionKey, poolIdx)
}

func (p *ProxyPool) Get(connectionKey string) (*httputil.ReverseProxy, error) {
	randomIndex := rand.Intn(p.poolSize)

	p.mu.Lock()
	defer p.mu.Unlock()

	proxy, ok := p.pool.Get(getLRUKey(connectionKey, randomIndex))
	if !ok {
		proxy, err := p.createProxyClient()
		if err != nil {
			return nil, err
		}

		p.pool.Add(getLRUKey(connectionKey, randomIndex), proxy)

		return proxy, nil
	}

	return proxy, nil
}

func (p *ProxyPool) Close(connectionKey string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for poolIdx := range p.poolSize {
		p.pool.Remove(getLRUKey(connectionKey, poolIdx))
	}
}

func (p *ProxyPool) TotalEstablishedConnections() uint64 {
	return p.totalEstablishedConnections.Load()
}
