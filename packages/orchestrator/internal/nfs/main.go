package nfs

import (
	"context"
	"net"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/willscott/go-nfs"
	helper "github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/gcs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/jailed"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/slogged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

const cacheLimit = 1024

type Proxy struct {
	server *nfs.Server
}

func getPrefixFromSandbox(sandboxes *sandbox.Map) jailed.GetPrefix {
	return func(_ context.Context, conn net.Conn, _ nfs.MountRequest) (string, error) {
		sbx, err := sandboxes.GetByHostPort(conn.RemoteAddr().String())
		if err != nil {
			return "", err
		}

		return sbx.Metadata.Runtime.SandboxID, nil
	}
}

func NewProxy(ctx context.Context, sandboxes *sandbox.Map, bucket *storage.BucketHandle) *Proxy {
	var handler nfs.Handler
	handler = gcs.NewNFSHandler(bucket)
	handler = helper.NewCachingHandler(handler, cacheLimit)
	handler = slogged.NewHandler(handler)
	handler = jailed.NewNFSHandler(handler, getPrefixFromSandbox(sandboxes))

	s := &nfs.Server{
		Handler: handler,
		Context: ctx,
	}

	return &Proxy{server: s}
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
