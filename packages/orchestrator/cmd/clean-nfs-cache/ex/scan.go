package ex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"

	"go.uber.org/zap"
)

func (c *Cleaner) FindCandidate(ctx context.Context) (*Candidate, error) {
	return c.findCandidate(ctx, []*Dir{&c.cacheRoot})
}

func (c *Cleaner) findCandidate(ctx context.Context, dirs []*Dir) (*Candidate, error) {
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

		// If we came across an empty directory, delete it before we get too far
		if d.IsEmpty() {
			if len(dirs) > 1 {
				c.mu.Lock()
				parent := dirs[len(dirs)-2]
				// remove this dir from parent
				for i, subdir := range parent.Dirs {
					if subdir.Name != d.Name {
						continue
					}
					parent.Dirs = append(parent.Dirs[:i], parent.Dirs[i+1:]...)

					break
				}
				*node = d
				c.mu.Unlock()
			}

			if !c.DryRun {
				c.timeit(ctx, "deleting empty dir (OS)", func() {
					if err := os.Remove(absPath); err == nil {
						c.RemoveDirC.Add(1)
					} else {
						c.Info(ctx, "failed to delete empty dir",
							zap.String("dir", absPath),
							zap.Error(err))
					}
				})
			}
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
		*node = d
		c.mu.Unlock()

		inc := 0
		for ; inc < len(d.Dirs) && inc < 10; inc++ {
			tryDir := &d.Dirs[(i+inc)%len(d.Dirs)]
			// Recurse into the chosen subdir, if nothing found there try again in this dir
			candidate, err := c.findCandidate(ctx, append(dirs, tryDir))
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

		return nil, fmt.Errorf("%w: nothing found to delete in %s", ErrNoFiles, absPath)
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
		Parent:    node,
		FullPath:  filepath.Join(absPath, f.Name),
		ATimeUnix: f.ATimeUnix,
		Size:      f.Size,
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
			// TODO: e.Type, e.Info, or e.IsDir?
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
			f, err := c.statInDir(df, path)
			if err != nil {
				return fmt.Errorf("failed to stat file %s: %w", path, err)
			}
			if f.ATimeUnix == 0 {
				f.ATimeUnix = 1 // mark as scanned even if atime is somehow zero
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
		return d.Files[i].ATimeUnix < d.Files[j].ATimeUnix
	})
}

func (d *Dir) AreFilesScanned() bool {
	return d.IsScanned() && len(d.Files) > 0 && d.Files[0].ATimeUnix != 0
}

func (d *Dir) IsScanned() bool {
	return d.Files != nil || d.Dirs != nil
}

func (d *Dir) IsEmpty() bool {
	return d.IsScanned() && len(d.Files) == 0 && len(d.Dirs) == 0
}
