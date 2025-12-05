package ex

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"go.uber.org/zap"
)

func (c *Cleaner) Statter(ctx context.Context, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-quitCh:
			return
		case req := <-c.statRequestCh:
			f, err := c.statInDir(req.df, req.name)
			req.f = f
			req.err = err
			req.response <- req
		}
	}
}

func (c *Cleaner) scanDir(path []*Dir) (*Dir, error) {
	d := path[len(path)-1]

	d.mu.Lock()
	switch d.state {
	case scanned:
		d.mu.Unlock()
		return d, nil
	case scanning:
		d.mu.Unlock()
		return nil, ErrBusy
	default:
		// continue
	}
	d.state = scanning
	d.mu.Unlock()

	absPath := c.abs(path, "")
	df, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open directory %s: %w", absPath, err)
	}
	defer df.Close()

	entries := []os.DirEntry{}
	for {
		c.ReadDirC.Add(1)
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

	// If the directory is empty, remove it from its parent and delete it
	if len(entries) == 0 && len(path) > 1 {
		c.removeEmptyDir(context.Background(), path)
		return nil, fmt.Errorf("%w: empty directory %s", ErrNoFiles, absPath)
	}

	dirs := make([]*Dir, 0)
	nFiles := 0
	var filenames []string
	for _, e := range entries {
		name := e.Name()
		t := e.Type()

		if t&os.ModeDir != 0 {
			dirs = append(dirs, NewDir(name))
			c.SeenDirC.Add(1)
		} else {
			// file
			c.SeenFileC.Add(1)
			nFiles++
			filenames = append(filenames, name)
		}
	}

	// submit all stat requests
	responseCh := make(chan *statReq, len(filenames))
	for _, name := range filenames {
		c.statRequestCh <- &statReq{
			df:       df,
			name:     name,
			response: responseCh,
		}
	}

	// get all stat responses
	err = nil
	files := make([]File, nFiles)
	for i := range nFiles {
		resp := <-responseCh
		if resp.err != nil {
			err = resp.err
			continue
		}
		files[i] = *resp.f
	}
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.Dirs = dirs
	d.Files = files
	d.sort()
	d.state = scanned
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
		} else {
			c.Info(ctx, "failed to delete empty dir",
				zap.String("dir", absPath),
				zap.Error(err))
		}
	}
}
