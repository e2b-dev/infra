//go:build darwin

package cleaner

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func (c *Cleaner) stat(path string) (*Candidate, error) {
	c.StatxC.Add(1)
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("could not stat info: %w", err)
	}

	actualStruct, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("stat did not return a syscall.Stat_t for %q: %T",
			stat.Name(), stat.Sys())
	}

	return &Candidate{
		Parent:    nil, // not relevant here
		FullPath:  path,
		Size:      uint64(stat.Size()),
		ATimeUnix: actualStruct.Atimespec.Sec,
		BTimeUnix: actualStruct.Birthtimespec.Sec,
	}, nil
}

func (c *Cleaner) statInDir(df *os.File, filename string) (*File, error) {
	c.StatxInDirC.Add(1)
	// performance on OS X doeas not matter, so just use the full stat
	cand, err := c.stat(filepath.Join(df.Name(), filename))
	if err != nil {
		return nil, err
	}

	return &File{
		Name:      filename,
		ATimeUnix: cand.ATimeUnix,
		Size:      cand.Size,
	}, nil
}
