package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// deleteWorker drains the builds selected for deletion and RemoveAll's each one
// whole.
func (c *Cleaner) deleteWorker(ctx context.Context, deleteCh <-chan build, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-deleteCh:
			if !ok {
				return
			}
			// A deletion can be verified (FF-gated) against the estimate before
			// removal, while the files still exist.
			if c.Verify {
				c.verifyBuild(ctx, b)
			}

			dur, ok := c.deleteBuild(ctx, b.uuid)
			if ok {
				c.Deleted.Add(1)
				c.BytesFreed.Add(b.size)
				c.metrics.recordDelete(ctx, dur, b.size, b.timestamp)
			} else {
				c.metrics.recordError(ctx, ValOpDelete)
			}
		}
	}
}

// deleteBuild RemoveAll's a build dir, returning the call's wall time and whether
// it succeeded. Honors DryRun (logs, doesn't remove).
func (c *Cleaner) deleteBuild(ctx context.Context, buildID string) (time.Duration, bool) {
	buildPath := filepath.Join(c.Path, buildID)
	if c.DryRun {
		c.Info(ctx, "would remove build dir (dry run)", zap.String("dir", buildPath))

		return 0, true
	}
	start := time.Now()
	err := os.RemoveAll(buildPath)
	dur := time.Since(start)
	if err != nil {
		c.Info(ctx, "failed to remove build dir", zap.String("dir", buildPath), zap.Error(err))

		return dur, false
	}

	return dur, true
}
