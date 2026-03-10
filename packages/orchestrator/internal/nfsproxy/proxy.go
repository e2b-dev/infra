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
	"github.com/google/uuid"
	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/logged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/recovery"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/volumes"
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

func getPrefixFromSandbox(sandboxes *sandbox.Map, filesystemsByType map[string]billy.Filesystem) chroot.GetPath {
	return func(remoteAddr net.Addr, request nfs.MountRequest) (string, error) {
		sbx, err := sandboxes.GetByHostPort(remoteAddr.String())
		if err != nil {
			return "", err
		}

		// normalize the mount path
		requestedPath := string(request.Dirpath)
		requestedPath = filepath.Clean(requestedPath)

		// at this point it must have at least a slash
		if !filepath.IsAbs(requestedPath) {
			return "", ErrMustMountAbsolutePath
		}

		// get the mount name from the path
		if requestedPath == "/" {
			return "", ErrCannotMountRoot
		}

		requestedPathParts := strings.Split(requestedPath, "/")
		volumeName := requestedPathParts[1]
		if volumeName == "" {
			return "", ErrCannotMountRoot
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
			return "", fmt.Errorf("failed to mount %q: %w", volumeName, ErrVolumeNotFound)
		}

		// get the filesystem for the mount type
		fileSystem, ok := filesystemsByType[volumeMount.Type]
		if !ok {
			return "", fmt.Errorf("failed to mount %q (%s): %w", volumeName, volumeMount.Type, ErrVolumeTypeNotSupported)
		}

		teamID, ok := internal.TryParseUUID(sbx.Metadata.Runtime.TeamID)
		if !ok {
			return "", ErrInvalidTeamID
		}

		if volumeMount.ID == uuid.Nil {
			return "", ErrVolumeID
		}

		pathParts := volumes.BuildVolumePathParts(teamID, volumeMount.ID)
		pathParts = append([]string{fileSystem.Root()}, pathParts...)
		if len(requestedPathParts) > 2 {
			pathParts = append(pathParts, requestedPathParts[2:]...)
		}

		return filepath.Join(pathParts...), nil
	}
}

func NewProxy(ctx context.Context, sandboxes *sandbox.Map, config cfg.Config) (*Proxy, error) {
	filesystemsByType := make(map[string]billy.Filesystem)

	for name, path := range config.PersistentVolumeMounts {
		filesystemsByType[name] = osfs.New(path)
	}

	handler := chroot.NewNFSHandler(getPrefixFromSandbox(sandboxes, filesystemsByType))
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
