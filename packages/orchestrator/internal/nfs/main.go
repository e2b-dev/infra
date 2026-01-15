package nfs

import (
	"context"
	"net"

	"github.com/go-git/go-billy/v5/memfs"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

type Proxy struct {
	server *nfs.Server
	cancel func()
}

func NewProxy() *Proxy {
	return &Proxy{}
}

func (p *Proxy) Serve(ctx context.Context, lis net.Listener) error {
	mem := memfs.New()

	handler := nfshelper.NewNullAuthHandler(mem)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1)

	ctx, p.cancel = context.WithCancel(ctx)
	p.server = &nfs.Server{
		Handler: cacheHelper,
		Context: ctx,
	}

	return p.server.Serve(lis)
}

func (p *Proxy) Stop(ctx context.Context) error {
	
}
