package nfsproxy

import (
	"context"
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

func getPrefixFromSandbox(sandboxes *sandbox.Map, config cfg.Config) jailed.GetPrefix {
	filesystemsByType := buildFilesystems(config.PersistentVolumeMounts)

	return func(_ context.Context, conn net.Conn, request nfs.MountRequest) (billy.Filesystem, string, error) {
		sbx, err := sandboxes.GetByHostPort(conn.RemoteAddr().String())
		if err != nil {
			return nil, "", err
		}

		dirPath := string(request.Dirpath)

		volumeName, ok := getMountName(dirPath)
		if !ok {
			return nil, "", fmt.Errorf("invalid volume name: %s", string(request.Dirpath))
		}

		volumeMount, ok := findVolumeMount(sbx.Config.VolumeMounts, volumeName)
		if !ok {
			return nil, "", fmt.Errorf("volume mount not found: %s", volumeName)
		}

		fileSystem, ok := findFilesystem(filesystemsByType, volumeMount)
		if !ok {
			return nil, "", fmt.Errorf("local mount path not found: %s", volumeMount)
		}

		return fileSystem, filepath.Join(sbx.Metadata.Runtime.TeamID, volumeName), nil
	}
}

func buildFilesystems(mounts map[string]string) map[string]billy.Filesystem {
	results := make(map[string]billy.Filesystem)

	for name, path := range mounts {
		results[name] = osfs.New(path)
	}

	return results
}

func getMountName(mountName string) (string, bool) {
	if mountName == "" {
		return "", false
	}

	mountName = filepath.Base(mountName)
	if mountName == "" {
		return "", false
	}

	return mountName, true
}

func findVolumeMount(mounts []sandbox.VolumeMountConfig, name string) (sandbox.VolumeMountConfig, bool) {
	for _, mount := range mounts {
		if mount.Name == name {
			return mount, true
		}
	}

	return sandbox.VolumeMountConfig{}, false
}

func findFilesystem(mounts map[string]billy.Filesystem, mount sandbox.VolumeMountConfig) (billy.Filesystem, bool) {
	fs, ok := mounts[mount.Type]

	return fs, ok
}

func getChangeFromFilesystem(fs billy.Filesystem) billy.Change {
	if ch, ok := fs.(billy.Chroot); ok {
		return oschange.NewChange(ch.Root())
	}

	panic(fmt.Sprintf("unexpected filesystem type: %v", fs))
}

func NewProxy(ctx context.Context, sandboxes *sandbox.Map, config cfg.Config) *Proxy {
	var handler nfs.Handler
	handler = jailed.NewNFSHandler(
		getPrefixFromSandbox(sandboxes, config),
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
