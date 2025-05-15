package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

const (
	// snapshotCacheDir is a tmpfs directory mounted on the host.
	// This is used for speed optimization as the final diff is copied to the persistent storage.
	snapshotCacheDir = "/mnt/snapshot-cache"

	maxParallelMemfileSnapshotting = 8
)

var snapshotCacheQueue = semaphore.NewWeighted(maxParallelMemfileSnapshotting)

type TemporaryMemfile struct {
	path    string
	closeFn func()
}

func AcquireTmpMemfile(
	ctx context.Context,
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
		path:    cacheMemfileFullSnapshotPath(buildID, randomID.String()),
		closeFn: releaseOnce,
	}, nil
}

func (f *TemporaryMemfile) Path() string {
	return f.path
}

func (f *TemporaryMemfile) Close() error {
	defer f.closeFn()

	return os.RemoveAll(f.path)
}

func cacheMemfileFullSnapshotPath(buildID string, randomID string) string {
	name := fmt.Sprintf("%s-%s-%s.full", buildID, MemfileName, randomID)

	return filepath.Join(snapshotCacheDir, name)
}
