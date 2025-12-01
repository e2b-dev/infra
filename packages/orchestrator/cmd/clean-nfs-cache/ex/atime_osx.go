//go:build darwin

package ex

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (c *Cleaner) fullStat(path string) (*Candidate, error) {
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

	age := time.Since(time.Unix(int64(actualStruct.Atimespec.Sec), int64(actualStruct.Atimespec.Nsec))).Minutes()
	bage := time.Since(time.Unix(int64(actualStruct.Birthtimespec.Sec), int64(actualStruct.Birthtimespec.Nsec))).Minutes()
	return &Candidate{
		Parent:      nil,   // not relevant here
		IsDir:       false, // not relevant: we are only called for files
		FullPath:    path,
		Size:        uint64(stat.Size()),
		AgeMinutes:  uint32(age),
		BAgeMinutes: uint32(bage),
	}, nil
}

func (c *Cleaner) quickStat(df *os.File, filename string) (*File, error) {
	// performance on OS X doeas not matter, so just use the full stat
	cand, err := c.fullStat(filepath.Join(df.Name(), filename))
	if err != nil {
		return nil, err
	}
	return &File{
		Name:       filename,
		AgeMinutes: cand.AgeMinutes,
		Size:       cand.Size,
	}, nil
}
