package nfsproxy

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/google/uuid"
	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/volumes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type FilesystemsCache struct {
	sandboxes   *sandbox.Map
	volumeTypes map[string]string

	cache map[string]*chroot.IsolatedFS
	mu    sync.Mutex

	cancel func()
}

func NewFilesystemsCache(sandboxes *sandbox.Map, config cfg.Config) *FilesystemsCache {
	return &FilesystemsCache{
		sandboxes:   sandboxes,
		volumeTypes: config.PersistentVolumeMounts,
		cancel:      func() {},
		cache:       make(map[string]*chroot.IsolatedFS),
	}
}

const gcLoopInterval = time.Minute

func (c *FilesystemsCache) Start(ctx context.Context) error {
	c.mu.Lock()
	ctx, c.cancel = context.WithCancel(ctx)
	defer c.cancel()
	c.mu.Unlock()

	ticker := time.NewTicker(gcLoopInterval)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.gc(ctx)
		}
	}
}

func (c *FilesystemsCache) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cancel()
}

func (c *FilesystemsCache) gc(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		sbxCount    int
		mountCount  int
		fsCount     int
		deleteCount int
	)

	usedVolumeIDs := make(map[string]struct{})

	for _, sbx := range c.sandboxes.Items() {
		sbxCount++
		for _, mount := range sbx.Config.VolumeMounts {
			mountCount++
			usedVolumeIDs[mount.ID.String()] = struct{}{}
		}
	}

	for path, fs := range c.cache {
		fsCount++

		volumeID, ok := fs.Metadata[volumeIDkey]
		if !ok {
			logger.L().Warn(ctx, "can't find the volume id metadata, skipping")

			continue
		}

		if _, ok := usedVolumeIDs[volumeID]; ok {
			continue
		}

		if err := fs.Close(); err != nil {
			logger.L().Warn(ctx, "failed to close filesystem",
				zap.String("path", fs.Root()),
				zap.String("volume_id", volumeID),
				zap.Error(err),
			)
		}

		deleteCount++
		delete(c.cache, path)
	}

	logger.L().Info(ctx, "filesystem garbage collection stats",
		zap.Int("sandboxes", sbxCount),
		zap.Int("mounts", mountCount),
		zap.Int("filesystems", fsCount),
		zap.Int("unique_volumes", len(usedVolumeIDs)),
		zap.Int("deleted_filesystems", deleteCount))
}

func (c *FilesystemsCache) chrootCallback(ctx context.Context, remoteAddr net.Addr, request nfs.MountRequest) (billy.Filesystem, error) {
	sbx, err := c.sandboxes.GetByHostPort(remoteAddr.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// normalize the mount path
	requestedPath := string(request.Dirpath)
	requestedPath = filepath.Clean(requestedPath)

	// at this point it must have at least a slash
	if !filepath.IsAbs(requestedPath) {
		return nil, ErrMustMountAbsolutePath
	}

	// get the mount name from the path
	if requestedPath == "/" {
		return nil, ErrCannotMountRoot
	}

	requestedPathParts := strings.Split(requestedPath, "/")
	volumeName := requestedPathParts[1]
	if volumeName == "" {
		return nil, ErrCannotMountRoot
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
		return nil, fmt.Errorf("failed to mount %q: %w", volumeName, ErrVolumeNotFound)
	}

	// get the filesystem for the mount type
	fileSystemRoot, ok := c.volumeTypes[volumeMount.Type]
	if !ok {
		return nil, fmt.Errorf("failed to mount %q (%s): %w", volumeName, volumeMount.Type, ErrVolumeTypeNotSupported)
	}

	teamID, ok := internal.TryParseUUID(sbx.Metadata.Runtime.TeamID)
	if !ok {
		return nil, ErrInvalidTeamID
	}

	if volumeMount.ID == uuid.Nil {
		return nil, ErrVolumeID
	}

	pathParts := volumes.BuildVolumePathParts(teamID, volumeMount.ID)
	pathParts = append([]string{fileSystemRoot}, pathParts...)
	if len(requestedPathParts) > 2 {
		pathParts = append(pathParts, requestedPathParts[2:]...)
	}

	fullPath := filepath.Join(pathParts...)

	return c.getFilesystem(ctx, fullPath, chroot.WithMetadata(volumeIDkey, volumeMount.ID.String()))
}

const volumeIDkey = "volume_id"

func (c *FilesystemsCache) getFilesystem(ctx context.Context, path string, opts ...chroot.Option) (billy.Filesystem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if fs, ok := c.cache[path]; ok {
		return fs, nil
	}

	fs, err := chroot.IsolateFileSystem(ctx, path, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to isolate filesystem: %w", err)
	}

	c.cache[path] = fs

	return fs, nil
}
