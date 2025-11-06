package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

var maxParallelMemfileSnapshotting = utils.Must(env.GetEnvAsInt("MAX_PARALLEL_MEMFILE_SNAPSHOTTING", 8))

func acquireTmpMemfile(
	ctx context.Context,
	config cfg.BuilderConfig,
	buildID string,
) (*TemporaryMemfile, error) {
	randomID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate identifier: %w", err)
	}

	err = snapshotCacheQueue.Acquire(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire cache: %w", err)
	}
	releaseOnce := sync.OnceFunc(func() {
		snapshotCacheQueue.Release(1)
	})

	return &TemporaryMemfile{
		path:    cacheMemfileFullSnapshotPath(config, buildID, randomID.String()),
		closeFn: releaseOnce,
	}, nil
}

var snapshotCacheQueue = semaphore.NewWeighted(int64(maxParallelMemfileSnapshotting))

type TemporaryMemfile struct {
	path    string
	closeFn func()
}

func (f *TemporaryMemfile) Path() string {
	return f.path
}

func (f *TemporaryMemfile) Close() error {
	defer f.closeFn()

	return os.RemoveAll(f.path)
}

func cacheMemfileFullSnapshotPath(config cfg.BuilderConfig, buildID string, randomID string) string {
	name := fmt.Sprintf("%s-%s-%s.full", buildID, paths.MemfileName, randomID)

	return filepath.Join(config.SnapshotCacheDir, name)
}
