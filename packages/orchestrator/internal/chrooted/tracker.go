package chrooted

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Tracker struct {
	sandboxes *sandbox.Map

	config cfg.Config
	cache  map[string]*Chrooted
	mu     sync.Mutex

	cancel func()
}

func NewTracker(sandboxes *sandbox.Map, config cfg.Config) *Tracker {
	return &Tracker{
		config:    config,
		sandboxes: sandboxes,
		cancel:    func() {},
		cache:     make(map[string]*Chrooted),
	}
}

const gcLoopInterval = time.Minute

func (c *Tracker) Start(ctx context.Context) error {
	c.mu.Lock()
	ctx, c.cancel = context.WithCancel(ctx)
	defer c.cancel()
	c.mu.Unlock()

	ticker := time.NewTicker(gcLoopInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.gc(ctx)
		}
	}
}

func (c *Tracker) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for path, fs := range c.cache {
		if err := fs.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close %q: %w", path, err))
		}
		delete(c.cache, path)
	}

	c.cancel()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *Tracker) gc(ctx context.Context) {
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

const volumeIDkey = "volume_id"

var ErrVolumeTypeNotFound = errors.New("volume type not found")

func (c *Tracker) Get(
	ctx context.Context,
	volumeType string,
	teamID, volumeID uuid.UUID,
) (*Chrooted, error) {
	volTypePath, ok := c.config.PersistentVolumeMounts[volumeType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrVolumeTypeNotFound, volumeType)
	}

	fullPath := BuildVolumeRootPath(volTypePath, teamID, volumeID)

	c.mu.Lock()
	defer c.mu.Unlock()

	if fs, ok := c.cache[fullPath]; ok {
		return fs, nil
	}

	fs, err := Chroot(ctx, fullPath, WithMetadata(volumeIDkey, volumeID.String()))
	if err != nil {
		return nil, fmt.Errorf("failed to isolate filesystem: %w", err)
	}

	c.cache[fullPath] = fs

	return fs, nil
}

func BuildVolumeRootPath(volumeTypeRoot string, teamID, volumeID uuid.UUID) string {
	return filepath.Join(
		volumeTypeRoot,
		fmt.Sprintf("team-%s", teamID),
		fmt.Sprintf("vol-%s", volumeID),
	)
}
