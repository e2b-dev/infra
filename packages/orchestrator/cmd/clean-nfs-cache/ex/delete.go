package ex

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

func (c *Cleaner) Deleter(ctx context.Context, toDelete <-chan *Candidate, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-quitCh:
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
		c.DeleteAlreadyGoneC.Add(1)

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
