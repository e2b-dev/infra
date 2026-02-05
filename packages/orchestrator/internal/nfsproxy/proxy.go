package nfsproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/jailed"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/logged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/oschange"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/recovery"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
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
)

func getPrefixFromSandbox(sandboxes *sandbox.Map, filesystemsByType map[string]billy.Filesystem) jailed.GetPrefix {
	return func(_ context.Context, remoteAddr net.Addr, request nfs.MountRequest) (billy.Filesystem, string, error) {
		sbx, err := sandboxes.GetByHostPort(remoteAddr.String())
		if err != nil {
			return nil, "", err
		}

		// normalize the mount path
		requestedPath := string(request.Dirpath)
		requestedPath = filepath.Clean(requestedPath)

		// at this point it must have at least a slash
		if !filepath.IsAbs(requestedPath) {
			return nil, "", ErrMustMountAbsolutePath
		}

		// get the mount name from the path
		if requestedPath == "/" {
			return nil, "", ErrCannotMountRoot
		}

		requestedPathParts := strings.Split(requestedPath, "/")
		volumeName := requestedPathParts[1]
		if volumeName == "" {
			return nil, "", ErrCannotMountRoot
		}

		// find the local volume mount
		var volumeMount *sandbox.VolumeMountConfig
		for _, sbxVolumeMount := range sbx.Config.VolumeMounts {
			if sbxVolumeMount.Name == volumeName {
				volumeMount = &sbxVolumeMount

				break
			}
		}
		if volumeMount == nil {
			return nil, "", fmt.Errorf("failed to mount %q: %w", volumeName, ErrVolumeNotFound)
		}

		// get the filesystem for the mount type
		fileSystem, ok := filesystemsByType[volumeMount.Type]
		if !ok {
			return nil, "", fmt.Errorf("failed to mount %q (%s): %w", volumeName, volumeMount.Type, ErrVolumeTypeNotSupported)
		}

		prefixParts := []string{sbx.Metadata.Runtime.TeamID, volumeName}
		if len(requestedPathParts) > 2 {
			prefixParts = append(prefixParts, requestedPathParts[2:]...)
		}

		return fileSystem, filepath.Join(prefixParts...), nil
	}
}

func getChangeFromFilesystem(fs billy.Filesystem) billy.Change {
	if ch, ok := fs.(billy.Chroot); ok {
		return oschange.NewChange(ch.Root())
	}

	panic(fmt.Sprintf("unexpected filesystem type: %v", fs))
}

func NewProxy(ctx context.Context, sandboxes *sandbox.Map, config cfg.Config) *Proxy {
	filesystemsByType := make(map[string]billy.Filesystem)

	for name, path := range config.PersistentVolumeMounts {
		filesystemsByType[name] = osfs.New(path)
	}

	var handler nfs.Handler
	handler = jailed.NewNFSHandler(
		getPrefixFromSandbox(sandboxes, filesystemsByType),
		getChangeFromFilesystem,
	)
	handler = helpers.NewCachingHandler(handler, cacheLimit)
	handler = logged.NewHandler(ctx, handler)
	handler = recovery.NewHandler(ctx, handler)

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
