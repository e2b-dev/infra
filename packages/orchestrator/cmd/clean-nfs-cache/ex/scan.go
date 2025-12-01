package ex

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// func (c *Cleaner) Scan(ctx context.Context, numWorkers int, quitCh chan struct{}, out chan []*Candidate) {
// 	workerPool := make(chan struct{}, numWorkers) // limit concurrency
// 	for i := 0; i < cap(workerPool); i++ {
// 		workerPool <- struct{}{}
// 	}
// 	wg := sync.WaitGroup{}

// 	cc := make([]*Candidate, 0, c.DeleteN)
// 	candidateCh := make(chan *Candidate)
// 	errCh := make(chan error)
// 	continuousErrors := 0
// 	n := 0

// 	for {
// 		start := time.Now()
// 		select {
// 		case <-ctx.Done():
// 			return

// 		case <-quitCh:
// 			wg.Wait()
// 			return

// 		case <-workerPool:
// 			wg.Add(1)
// 			go func() {
// 				defer wg.Done()
// 				candidate, err := c.FindCandidate()
// 				if err != nil {
// 					errCh <- err
// 				} else {
// 					candidateCh <- candidate
// 				}
// 				workerPool <- struct{}{}
// 			}()

// 		case candidate := <-candidateCh:
// 			logger.L().Debug(ctx, "received candidate to delete",
// 				zap.Duration("waited", time.Since(start)),
// 				zap.Uint32("age_minutes", candidate.AgeMinutes),
// 				zap.Uint64("size_bytes", candidate.Size),
// 				zap.String("name", filepath.Base(candidate.FullPath)))

// 			continuousErrors = 0
// 			n++
// 			cc = append(cc, candidate)
// 			if n >= c.BatchN {
// 				// This will block if the previous batch is still being processed
// 				out <- cc
// 				cc = cc[:0]
// 				n = 0
// 			}

// 		case err := <-errCh:
// 			logger.L().Info(ctx, "error during scanning",
// 				zap.Error(err))
// 			continuousErrors++
// 			if continuousErrors > 10 {
// 				logger.L().Error(ctx, "too many continuous errors, stopping scan")
// 				close(quitCh)
// 			}
// 		}
// 	}
// }

func (c *Cleaner) FindCandidate() (*Candidate, error) {
	return c.findCandidate([]*Dir{&c.root})
}

func (c *Cleaner) findCandidate(dirs []*Dir) (*Candidate, error) {
	var err error
	var df *os.File
	defer func() {
		if df != nil {
			df.Close()
		}
	}()
	node := dirs[len(dirs)-1] // The original pointer to update later, always have at least 1

	c.mu.RLock()
	d := *node
	absPath := c.abs(dirs, "")
	c.mu.RUnlock()

	if !d.IsScanned() {
		df, err = os.Open(absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open directory %s: %w", absPath, err)
		}
		err = c.scanDir(df, &d, true, c.AggressiveStat)
		if err != nil {
			return nil, err
		}
	}

	if d.IsEmpty() {
		return nil, fmt.Errorf("%w: nothing left to delete in %s", ErrNoFiles, absPath)
	}

	// Get a random item (dir or file)
	itemCount := len(d.Files) + len(d.Dirs)
	i := rand.Intn(itemCount)

	if i < len(d.Dirs) {
		df.Close() // do not hold directory handles open across recursions
		df = nil

		c.mu.Lock()
		d.Name = node.Name // TODO why is this needed?
		*node = d
		c.mu.Unlock()

		inc := 0
		for inc = 0; inc < len(d.Dirs) && inc < maxErrorRetries; inc++ {
			tryDir := &d.Dirs[(i+inc)%len(d.Dirs)]
			// Recurse into the chosen subdir, if nothing found there try again in this dir
			candidate, err := c.findCandidate(append(dirs, tryDir))
			switch {
			case err == nil:
				return candidate, nil
			case errors.Is(err, ErrNoFiles):
				// try again in this dir
				continue
			default:
				return nil, err
			}
		}

		if inc >= maxErrorRetries {
			return nil, fmt.Errorf("%w: too many retries scanning subdirectories in %s", ErrMaxRetries, absPath)
		} else {
			return nil, fmt.Errorf("%w: nothing left to delete in %s", ErrNoFiles, absPath)
		}
	}

	// chose the oldest file, make sure we have metadata for all files in the dir
	if !d.AreFilesScanned() {
		if df == nil {
			df, err = os.Open(absPath)
			if err != nil {
				return nil, fmt.Errorf("failed to open directory %s: %w", absPath, err)
			}
			defer df.Close()
		}

		err = c.scanDir(df, &d, false, true)
		if err != nil {
			return nil, err
		}
	}

	// remove this file from the node.
	c.mu.Lock()
	f := d.Files[len(d.Files)-1] // oldest file
	d.Files = d.Files[:len(d.Files)-1]
	*node = d
	c.mu.Unlock()

	candidate := &Candidate{
		Parent:     node,
		FullPath:   filepath.Join(absPath, f.Name),
		AgeMinutes: f.AgeMinutes,
		Size:       f.Size,
	}

	return candidate, nil
}

func (c *Cleaner) scanDir(df *os.File, d *Dir, readDir bool, stat bool) error {
	if !readDir && !stat {
		return nil
	}

	if readDir && !d.IsScanned() {
		entries := []os.DirEntry{}
		for {
			// track each individual ReadDir syscall (important for NFS)
			c.ReadDirC.Add(1)
			e, err := df.ReadDir(2048)
			if len(e) != 0 {
				entries = append(entries, e...)
			}
			switch {
			case err == io.EOF:
				// explicit EOF - we're done
			case err != nil:
				return fmt.Errorf("failed to read directory %s: %w", df.Name(), err)
			case len(e) < 2048:
				// got fewer than requested with no error - we're done
			default:
				// got exactly 2048, keep reading
				continue
			}
			break
		}

		// always initialize files map to distinguish "not scanned" vs "empty"
		d.Files = make([]File, 0)

		for _, e := range entries {
			name := e.Name()
			t := e.Type()
			if t&os.ModeDir != 0 {
				d.Dirs = append(d.Dirs, Dir{Name: name})
				c.SeenDirC.Add(1)
			} else {
				d.Files = append(d.Files, File{Name: name})
				c.SeenFileC.Add(1)
			}
		}
	}

	if stat && !d.AreFilesScanned() {
		for i, f := range d.Files {
			path := f.Name
			f, err := c.quickStat(df, path)
			if err != nil {
				return fmt.Errorf("failed to stat file %s: %w", path, err)
			}
			if f.AgeMinutes == 0 {
				f.AgeMinutes = 1 // mark as scanned even if atime is somehow zero
			}
			d.Files[i] = *f
		}
	}

	d.Sort()
	return nil
}

func (c *Cleaner) abs(path []*Dir, name string) string {
	join := c.basePath
	for _, p := range path {
		join = filepath.Join(join, p.Name)
	}
	if name != "" {
		join = filepath.Join(join, name)
	}
	return join
}

func (d *Dir) Sort() {
	// sort the dirs by name
	sort.Slice(d.Dirs, func(i, j int) bool {
		return d.Dirs[i].Name < d.Dirs[j].Name
	})

	// sort the files by age, oldest last
	sort.Slice(d.Files, func(i, j int) bool {
		return d.Files[i].AgeMinutes < d.Files[j].AgeMinutes
	})
}

func (d *Dir) AreFilesScanned() bool {
	return d.IsScanned() && len(d.Files) > 0 && d.Files[0].AgeMinutes != 0
}

func (d *Dir) IsScanned() bool {
	return d.Files != nil || d.Dirs != nil
}

func (d *Dir) IsEmpty() bool {
	return d.IsScanned() && len(d.Files) == 0 && len(d.Dirs) == 0
}

func CreateTestDir(path string, nDirs int, nFiles int, fsize int) {
	os.MkdirAll(path, 0755)

	for i := 0; i < nDirs; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i))
		err := os.Mkdir(dirPath, 0755)
		if err != nil {
			panic(err)
		}
	}

	for i := 0; i < nFiles; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i%nDirs))
		filePath := filepath.Join(dirPath, fmt.Sprintf("file%d.txt", i))
		err := os.WriteFile(filePath, []byte(""), 0644)
		if err == nil {
			err = os.Truncate(filePath, int64(fsize))
		}
		if err != nil {
			panic(err)
		}
		tt := time.Now().Add(-1 * time.Duration(i) * time.Minute)
		err = os.Chtimes(filePath, tt, tt)
		if err != nil {
			panic(err)
		}
	}
}
