package nfsproxy

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/logged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/recovery"
)

const cacheLimit = 1024

type Proxy struct {
	server *nfs.Server
}

var (
	ErrCannotMountRoot        = errors.New("cannot mount root")
	ErrVolumeTypeNotSupported = errors.New("volume type not supported")
	ErrVolumeNotFound         = errors.New("volume not found")
	ErrMustMountAbsolutePath  = errors.New("must mount absolute path")
	ErrInvalidTeamID          = errors.New("invalid team ID")
	ErrVolumeID               = errors.New("invalid volume ID")
)

func NewProxy(ctx context.Context, cache *FilesystemsCache) (*Proxy, error) {
	// actual nfs handler
	var handler nfs.Handler = chroot.NewNFSHandler(cache.chrootCallback)

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
