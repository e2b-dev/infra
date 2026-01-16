package nfs

import (
	"context"
	"errors"
	"net"

	"cloud.google.com/go/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/willscott/go-nfs"
	helper "github.com/willscott/go-nfs/helpers"
)

const cacheLimit = 1024

type Proxy struct {
	sandboxes *sandbox.Map
	server    *nfs.Server
	cancel    func()
}

func NewProxy(sandboxes *sandbox.Map) *Proxy {
	return &Proxy{sandboxes: sandboxes}
}

func (p *Proxy) Start(ctx context.Context, lis net.Listener, client *storage.Client) error {
	handler := newSandboxJailsHandler(p.sandboxes, client, "e2b-staging-joe-fc-build-cache")
	handler = helper.NewCachingHandler(handler, cacheLimit)
	handler = newErrorReporter(handler)

	ctx, p.cancel = context.WithCancel(ctx)
	p.server = &nfs.Server{
		Handler: handler,
		Context: ctx,
	}

	return p.server.Serve(lis)
}

var ErrServerStopped = errors.New("server is stopped")

func (p *Proxy) Stop(_ context.Context) error {
	if p.cancel == nil {
		return ErrServerStopped
	}

	p.cancel()
	p.cancel = nil

	return nil
}
