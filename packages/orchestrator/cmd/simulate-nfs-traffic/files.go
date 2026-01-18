package main

import (
	"fmt"
	"io/fs"
	"math/rand"
	"path/filepath"
	"slices"
)

func (p *processor) findFiles() error {
	var paths []string

	err := filepath.WalkDir(p.path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// If fixedSize is specified, check the file size
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() != expectedFileSize {
			return nil
		}

		paths = append(paths, path)

		if p.limitFileCount > 0 && len(paths) >= p.limitFileCount {
			return filepath.SkipAll
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to find files in %q: %w", p.path, err)
	}

	p.allFiles = paths

	fmt.Printf("found %d files\n", len(p.allFiles))

	return nil
}

func removeAtIndex[T any](items []T, idx int) []T {
	return slices.Delete(items, idx, idx+1)
}

type files struct {
	rand             *rand.Rand
	paths            []string
	allowRepeatReads bool
}

func (f *files) selectFile() (string, error) {
	if len(f.paths) == 0 {
		return "", fmt.Errorf("no files found")
	}

	idx := f.rand.Intn(len(f.paths))
	path := f.paths[idx]

	if !f.allowRepeatReads {
		// remove path from paths
		f.paths = removeAtIndex(f.paths, idx)
	}

	return path, nil
}
