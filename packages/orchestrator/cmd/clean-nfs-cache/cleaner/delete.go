package cleaner

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

func (c *Cleaner) Deleter(ctx context.Context, toDelete <-chan *Candidate, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-toDelete:
			c.deleteFile(ctx, d)
		}
	}
}

func (c *Cleaner) deleteFile(ctx context.Context, candidate *Candidate) {
	// Best-effort: get current metadata to detect atime changes or if file is gone
	meta, err := c.stat(candidate.FullPath)
	c.DeleteAttemptC.Add(1)

	switch {
	case err != nil:
		if !os.IsNotExist(err) {
			c.Info(ctx, "error stating file before delete", zap.Error(err))
			c.DeleteAlreadyGoneC.Add(1)
		} else {
			c.DeleteErrC.Add(1)
		}

	case meta.ATimeUnix == candidate.ATimeUnix:
		c.RemoveC.Add(1)
		if !c.DryRun {
			c.timeit(ctx,
				fmt.Sprintf("delete file aged %v: %s", time.Since(time.Unix(candidate.ATimeUnix, 0)), candidate.FullPath),
				func() {
					err = os.Remove(candidate.FullPath)
				})
		}
		if err == nil {
			c.DeletedBytes.Add(candidate.Size)
			c.root.mu.Lock()
			c.DeletedAge = append(c.DeletedAge, time.Since(time.Unix(candidate.ATimeUnix, 0)))
			c.root.mu.Unlock()
		}

	default:
		c.DeleteSkipC.Add(1)
	}
}
