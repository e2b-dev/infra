package nfsproxy

import (
	"context"
	"net"
	"strings"

	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/logged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/recovery"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

const cacheLimit = 1024

type Proxy struct {
	server *nfs.Server
}

func NewProxy(ctx context.Context, builder *chrooted.Builder, sandboxes *sandbox.Map) (*Proxy, error) {
	// actual nfs handler
	var handler nfs.Handler = chroot.NewNFSHandler(builder, sandboxes)

	// wrap the handler in middleware
	handler = helpers.NewCachingHandler(handler, cacheLimit)
	handler = logged.WrapWithLogging(ctx, handler)
	handler = recovery.WrapWithRecovery(ctx, handler)

	s := &nfs.Server{
		Handler: handler,
		Context: ctx,
	}

	return &Proxy{server: s}, nil
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
