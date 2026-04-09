package nfsproxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/quota"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const cacheLimit = 1024

var setLogLevelOnce sync.Once

var nfsMeter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy")

var (
	callsCounter = utils.Must(nfsMeter.Int64Counter("orchestrator.nfsproxy.calls.total",
		metric.WithDescription("Total number of calls to the NFS proxy"),
		metric.WithUnit("1")))
	durationHistogram = utils.Must(nfsMeter.Int64Histogram("orchestrator.nfsproxy.call.duration",
		metric.WithDescription("Duration of calls to the NFS proxy"),
		metric.WithUnit("ms")))
)

type Proxy struct {
	config cfg.Config
	server *nfs.Server
}

func NewProxy(ctx context.Context, builder *chrooted.Builder, sandboxes *sandbox.Map, tracker *quota.Tracker, config cfg.Config) (*Proxy, error) {
	setLogLevelOnce.Do(func() {
		nfs.Log.SetLevel(config.NFSLogLevel)
	})

	// actual nfs handler
	var (
		handler nfs.Handler
		err     error
	)
	handler, err = chroot.NewNFSHandler(builder, sandboxes, tracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create chroot NFS handler: %w", err)
	}

	// wrap the handler in caching
	handler = helpers.NewCachingHandler(handler, cacheLimit)

	// build skip maps for conditional tracing/logging
	skipOps := make(map[string]bool)
	if !config.RecordStatCalls {
		skipOps["FS.Stat"] = true
		skipOps["FS.Lstat"] = true
	}
	if !config.RecordHandleCalls {
		skipOps["Handler.ToHandle"] = true
		skipOps["Handler.FromHandle"] = true
		skipOps["Handler.InvalidateHandle"] = true
	}

	// build interceptor chain (order matters: recovery should be first to catch panics from all others)
	var interceptors []middleware.Interceptor
	interceptors = append(interceptors, middleware.Recovery())

	if config.Tracing {
		interceptors = append(interceptors, middleware.Tracing(skipOps))
	}

	if config.Metrics {
		interceptors = append(interceptors, middleware.Metrics(callsCounter, durationHistogram))
	}

	if config.Logging {
		interceptors = append(interceptors, middleware.Logging(skipOps))
	}

	chain := middleware.NewChain(interceptors...)
	handler = middleware.WrapHandler(handler, chain)

	s := &nfs.Server{
		Handler:      handler,
		Context:      ctx,
		OnConnect:    onConnect,
		OnDisconnect: onDisconnect,
	}

	return &Proxy{
		config: config,
		server: s,
	}, nil
}

func (p *Proxy) Serve(lis net.Listener) error {
	if err := p.server.Serve(lis); err != nil {
		if strings.Contains(err.Error(), "use of closed network connection") {
			return nil
		}

		return err
	}

	return nil
}
