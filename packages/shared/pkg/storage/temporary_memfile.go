package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var maxParallelMemfileSnapshotting = utils.Must(env.GetEnvAsInt("MAX_PARALLEL_MEMFILE_SNAPSHOTTING", 8))

var snapshotCacheQueue = semaphore.NewWeighted(int64(maxParallelMemfileSnapshotting))

type TemporaryMemfile struct {
	path    string
	closeFn func()
}

func AcquireTmpMemfile(
	ctx context.Context,
	config Config,
	buildID string,
) (*TemporaryMemfile, error) {
	randomID := uuid.NewString()

	err := snapshotCacheQueue.Acquire(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire cache: %w", err)
	}
	releaseOnce := sync.OnceFunc(func() {
		snapshotCacheQueue.Release(1)
	})

	return &TemporaryMemfile{
		path:    cacheMemfileFullSnapshotPath(config, buildID, randomID),
		closeFn: releaseOnce,
	}, nil
}

func (f *TemporaryMemfile) Path() string {
	return f.path
}

func (f *TemporaryMemfile) Close() error {
	defer f.closeFn()

	return os.Remove(f.path)
}

func cacheMemfileFullSnapshotPath(config Config, buildID string, randomID string) string {
	name := fmt.Sprintf("%s-%s-%s.full", buildID, MemfileName, randomID)

	return filepath.Join(config.SnapshotCacheDir, name)
}
