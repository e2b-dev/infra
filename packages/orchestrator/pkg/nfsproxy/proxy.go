package nfsproxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/o11y"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
)

const cacheLimit = 1024

var setLogLevelOnce sync.Once

type Proxy struct {
	config cfg.Config
	server *nfs.Server
}

func NewProxy(
	ctx context.Context,
	builder *chrooted.Builder,
	sandboxes *sandbox.Map,
	config cfg.Config,
) (*Proxy, error) {
	setLogLevelOnce.Do(func() {
		nfs.Log.SetLevel(config.NFSLogLevel)
	})

	// actual nfs handler
	var (
		handler nfs.Handler
		err     error
	)
	handler, err = chroot.NewNFSHandler(builder, sandboxes)
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

	interceptors := buildInterceptors(config, skipOps)
	interceptors = append(interceptors, config.Interceptors...)
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

func buildInterceptors(config cfg.Config, skipOps map[string]bool) []middleware.Interceptor {
	// build interceptor chain (order matters: recovery should be first to catch panics from all others)
	var interceptors []middleware.Interceptor
	interceptors = append(interceptors, Recovery())

	if config.Tracing {
		interceptors = append(interceptors, o11y.Tracing(skipOps))
	}

	if config.Metrics {
		interceptors = append(interceptors, o11y.Metrics(skipOps))
	}

	if config.Logging {
		interceptors = append(interceptors, o11y.Logging(skipOps))
	}

	return interceptors
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
