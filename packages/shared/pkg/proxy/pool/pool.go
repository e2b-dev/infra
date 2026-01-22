package pool

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// hostConnectionSplit is used for splitting the total number of connections between the hosts.
// This is used to limit the number of connections per host to avoid exhausting the number of available via one host.
// The total number of connection per host will be total connections / hostConnectionSplit.
// If the total connections is lower than hostConnectionSplit, the total connections will be used for each host.
const hostConnectionSplit = 4

// ClientProxyConnectionKey is a constant connection key for client proxy, because we don't have to separate connection pools
// as we need to do when connecting to sandboxes (from orchestrator proxy) to prevent reuse of pool connections
// by different sandboxes cause failed connections.
const ClientProxyConnectionKey = "client-proxy"

type ProxyPool struct {
	pool                  *smap.Map[*ProxyClient]
	maxClientConns        int
	maxConnectionAttempts int
	idleTimeout           time.Duration
	totalConnsCounter     atomic.Uint64
	currentConnsCounter   atomic.Int64
	disableKeepAlives     bool
}

func New(maxClientConns int, maxConnectionAttempts int, idleTimeout time.Duration, disableKeepAlives bool) *ProxyPool {
	return &ProxyPool{
		pool:                  smap.New[*ProxyClient](),
		maxClientConns:        maxClientConns,
		maxConnectionAttempts: maxConnectionAttempts,
		idleTimeout:           idleTimeout,
		disableKeepAlives:     disableKeepAlives,
	}
}

func (p *ProxyPool) Get(ctx context.Context, d *Destination) *ProxyClient {
	return p.pool.Upsert(d.ConnectionKey, nil, func(exist bool, inMapValue *ProxyClient, _ *ProxyClient) *ProxyClient {
		if exist && inMapValue != nil {
			return inMapValue
		}

		withFields := make([]zap.Field, 0)

		if d.IncludeSandboxIdInProxyErrorLogger {
			withFields = append(withFields, logger.WithSandboxID(d.SandboxId), logger.WithExecutionID(d.ConnectionKey))
		}

		if d.MaskRequestHost != nil {
			withFields = append(withFields, zap.Stringp("mask_request_host", d.MaskRequestHost))
		}

		l := logger.L().With(withFields...)

		stdLogger, err := zap.NewStdLogAt(l.Detach(ctx), zap.WarnLevel)
		if err != nil {
			l.Warn(ctx, "failed to create logger", zap.Error(err))
		}

		l.Debug(ctx, "creating proxy client in pool")

		//nolint:contextcheck // Function `newProxyClient->newProxyClient$4` should pass the context parameter, but there is no reason to
		return newProxyClient(
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
			stdLogger,
			p.disableKeepAlives,
		)
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
