package nfs

import (
	"context"
	"net"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/gcs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/jailed"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/slogged"
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

func (p *Proxy) Start(ctx context.Context, lis net.Listener, bucket *storage.BucketHandle) error {
	var handler nfs.Handler
	handler = gcs.NewNFSHandler(ctx, bucket)
	handler = helper.NewCachingHandler(handler, cacheLimit)
	handler = slogged.NewHandler(handler)
	handler = jailed.NewNFSHandler(handler, p.getPrefixFromSandbox)

	ctx, p.cancel = context.WithCancel(ctx)
	p.server = &nfs.Server{
		Handler: handler,
		Context: ctx,
	}

	if err := p.server.Serve(lis); err != nil {
		if !strings.Contains(err.Error(), "use of closed network connection") {
			return err
		}
	}

	return nil
}

func (p *Proxy) getPrefixFromSandbox(conn net.Conn) (string, error) {
	sbx, err := p.sandboxes.GetByHostPort(conn.RemoteAddr().String())
	if err != nil {
		return "", err
	}

	return sbx.Metadata.Runtime.SandboxID, nil
}
