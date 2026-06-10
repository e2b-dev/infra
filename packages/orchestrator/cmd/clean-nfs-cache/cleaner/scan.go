package cleaner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

func (c *Cleaner) Scanner(ctx context.Context, candidateCh chan<- *Candidate, errCh chan<- error, done *sync.WaitGroup) {
	defer done.Done()
	continuousErrors := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
			candidate, err := c.FindCandidate(ctx)

			switch {
			case err == nil:
				continuousErrors = 0
				c.FileC.Add(-1)
				candidateCh <- candidate

			case errors.Is(err, ErrBusy):
				// We tried a busy directory, just retry
				c.metrics.ScanBusy.Add(ctx, 1)
				time.Sleep(1 * time.Millisecond)

				continue

			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// Shutdown in progress; the outer select will exit on the
				// next iteration. Don't log it as an error or pollute errCh.
				return

			default:
				if !errors.Is(err, ErrNoFiles) {
					c.Info(ctx, "error during scanning",
						zap.Int("continuousCount", continuousErrors),
						zap.Error(err))
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

func (c *Cleaner) Statter(ctx context.Context, done *sync.WaitGroup) {
	defer done.Done()
	statInDirAttrs := metric.WithAttributes(attribute.String(AttrSource, ValSrcInDir))
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.statRequestCh:
			start := time.Now()
			f, err := c.statInDir(req.df, req.name)
			c.metrics.StatDuration.Record(ctx, time.Since(start).Milliseconds(), statInDirAttrs)
			c.metrics.StatOps.Add(ctx, 1, statInDirAttrs)
			req.f = f
			req.err = err
			req.response <- req
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

func (d *Dir) randomSubdirOrOldestFile() (randomCandidate *File, randomSubdir *Dir, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.Files) == 0 && len(d.Dirs) == 0 {
		return nil, nil, fmt.Errorf("no candidates found in %s: %w", d.Name, ErrNoFiles)
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

func (c *Cleaner) scanDir(ctx context.Context, path []*Dir) (out *Dir, err error) {
	d := path[len(path)-1]

	d.mu.Lock()

	switch d.state {
	case dirStatScanned:
		d.mu.Unlock()

		return d, nil

	case dirStateScanning:
		d.mu.Unlock()

		return nil, fmt.Errorf("%w: directory %s is busy being scanned", ErrBusy, c.abs(path, ""))

	default:
		// continue
	}
	d.state = dirStateScanning
	d.mu.Unlock()

	defer func() {
		if err != nil {
			// on error, mark dir as not scanned
			d.mu.Lock()
			d.state = dirStateInitial
			d.mu.Unlock()
		}
	}()

	absPath := c.abs(path, "")
	df, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open directory %s: %w", absPath, err)
	}
	defer df.Close()

	depth := int64(len(path) - 1)
	depthAttr := attribute.Int64(AttrDepth, depth)
	depthAttrSet := metric.WithAttributes(depthAttr)

	readdirStart := time.Now()
	entries := []os.DirEntry{}
	for {
		c.ReadDirC.Add(1)
		c.metrics.ReaddirOps.Add(ctx, 1, depthAttrSet)
		e, err := df.ReadDir(2048)
		if len(e) != 0 {
			entries = append(entries, e...)
		}
		switch {
		case err == io.EOF:
			// explicit EOF - we're done
		case err != nil:
			return nil, fmt.Errorf("failed to read directory %s: %w", df.Name(), err)
		case len(e) < 2048:
			// got fewer than requested with no error - we're done
		default:
			// got exactly 2048, keep reading
			continue
		}

		break
	}
	c.metrics.ScanReaddirDuration.Record(ctx, time.Since(readdirStart).Milliseconds(), depthAttrSet)

	// If the directory is empty, remove it from its parent and delete it
	if len(entries) == 0 && len(path) > 1 {
		c.removeEmptyDir(ctx, path)

		return nil, fmt.Errorf("%w: empty directory %s", ErrNoFiles, absPath)
	}

	dirs := make([]*Dir, 0)
	var filenames []string
	for _, e := range entries {
		name := e.Name()
		t := e.Type()

		if t&os.ModeDir != 0 {
			dirs = append(dirs, NewDir(name))
			c.DirC.Add(1)
		} else {
			filenames = append(filenames, name)
		}
	}

	// Record entry-count distributions by kind so we can see, per depth,
	// whether dirs (e.g. BuildID UUIDs at the top level) or files (chunks
	// inside a BuildID) dominate.
	c.metrics.ScanEntries.Record(ctx, int64(len(dirs)),
		metric.WithAttributes(depthAttr, attribute.String(AttrKind, ValKindDir)))
	c.metrics.ScanEntries.Record(ctx, int64(len(filenames)),
		metric.WithAttributes(depthAttr, attribute.String(AttrKind, ValKindFile)))

	// Submit stat requests using the directory fd so Statter can use
	// fd-relative statx — on NFS this avoids per-component LOOKUP RPCs.
	//
	// Once a Statter has pulled a request off statRequestCh it will always
	// send a response (it does not re-check ctx mid-processing). To make
	// the deferred df.Close() safe, we must drain a response for every
	// successfully-submitted request before returning, even when ctx is
	// canceled mid-loop. responseCh is buffered to len(filenames) so a
	// Statter's send back never blocks.
	statPhaseStart := time.Now()
	responseCh := make(chan *statReq, len(filenames))
	submitted := 0
submitLoop:
	for _, name := range filenames {
		select {
		case <-ctx.Done():
			err = ctx.Err()

			break submitLoop
		case c.statRequestCh <- &statReq{df: df, name: name, response: responseCh}:
			submitted++
		}
	}

	files := make([]File, 0, submitted)
	for range submitted {
		resp := <-responseCh
		switch {
		case resp.err != nil:
			if err == nil {
				err = resp.err
			}
		case err == nil:
			files = append(files, *resp.f)
		}
	}
	c.metrics.ScanStatPhaseDuration.Record(ctx, time.Since(statPhaseStart).Milliseconds(), depthAttrSet)
	if err != nil {
		return nil, err
	}
	c.FileC.Add(int64(len(files)))

	d.mu.Lock()
	d.Dirs = dirs
	d.Files = files
	d.sort()
	d.state = dirStatScanned
	d.mu.Unlock()

	return d, nil
}

func (c *Cleaner) removeEmptyDir(ctx context.Context, path []*Dir) {
	d := path[len(path)-1]
	parent := path[len(path)-2]
	absPath := c.abs(path, "")

	parent.mu.Lock()
	// remove this dir from its parent
	for i, subdir := range parent.Dirs {
		if subdir.Name != d.Name {
			continue
		}
		parent.Dirs = append(parent.Dirs[:i], parent.Dirs[i+1:]...)

		break
	}
	parent.mu.Unlock()

	if !c.DryRun {
		if err := os.Remove(absPath); err == nil {
			c.RemoveDirC.Add(1)
			c.metrics.RmdirOps.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrResult, ValResultOk)))
		} else {
			c.metrics.RmdirOps.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrResult, ValResultErr)))
			c.Info(ctx, "failed to delete empty dir",
				zap.String("dir", absPath),
				zap.Error(err))
		}
	}
}
