package ex

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"
)

func (c *Cleaner) Scanner(ctx context.Context, candidateCh chan<- *Candidate, errCh chan<- error, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	continuousErrors := 0
	for {
		select {
		case <-quitCh:
			return
		default:
			var candidate *Candidate
			var err error
			c.timeit(ctx, "find candidate", func() {
				candidate, err = c.FindCandidate(ctx)
			})

			switch {
			case err == nil:
				continuousErrors = 0
				candidateCh <- candidate

			case errors.Is(err, ErrBusy):
				// We tried a busy directory, just retry
				time.Sleep(1 * time.Millisecond)

				continue

			default:
				if !errors.Is(err, ErrNoFiles) {
					c.Info(ctx, "error during scanning", zap.Error(err))
				}
				continuousErrors++
				if continuousErrors >= c.MaxErrorRetries {
					errCh <- ErrMaxRetries

					return
				}
				errCh <- err
			}
		}
	}
}

func (c *Cleaner) FindCandidate(ctx context.Context) (*Candidate, error) {
	return c.findCandidate(ctx, []*Dir{c.root})
}

func (c *Cleaner) findCandidate(ctx context.Context, path []*Dir) (*Candidate, error) {
	d, err := c.scanDir(ctx, path)
	if err != nil {
		return nil, err
	}

	f, subDir, err := d.randomSubdirOrOldestFile()
	switch {
	case err != nil:
		return nil, err

	case f == nil:
		return c.findCandidate(ctx, append(path, subDir))

	default:
		return &Candidate{
			Parent:    d,
			FullPath:  c.abs(path, f.Name),
			ATimeUnix: f.ATimeUnix,
			Size:      f.Size,
		}, nil
	}
}

func (d *Dir) randomSubdirOrOldestFile() (cadidate *File, randomSubdir *Dir, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.Files) == 0 && len(d.Dirs) == 0 {
		return nil, nil, ErrNoFiles
	}
	itemCount := len(d.Dirs) + len(d.Files)
	i := rand.Intn(itemCount)

	if i < len(d.Dirs) {
		return nil, d.Dirs[i], nil
	}

	// file needs to be unlinked before it's returned
	f := d.Files[len(d.Files)-1]
	d.Files = d.Files[:len(d.Files)-1]

	return &f, nil, nil
}
