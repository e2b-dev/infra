//go:build linux

package ex

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func (c *Cleaner) stat(fullPath string) (*Candidate, error) {
	c.StatxC.Add(1)
	var statx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, fullPath,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME|unix.STATX_BTIME|unix.STATX_SIZE,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", fullPath, err)
	}

	age := time.Since(time.Unix(int64(statx.Atime.Sec), int64(statx.Atime.Nsec))).Minutes()
	bage := time.Since(time.Unix(int64(statx.Btime.Sec), int64(statx.Btime.Nsec))).Minutes()
	return &Candidate{
		Parent:      nil,   // not relevant here
		IsDir:       false, // not relevant: we are only called for files
		FullPath:    fullPath,
		Size:        statx.Size,
		AgeMinutes:  uint32(age),
		BAgeMinutes: uint32(bage),
	}, nil
}

func (c *Cleaner) statInDir(df *os.File, filename string) (*File, error) {
	c.StatxC.Add(1)
	var statx unix.Statx_t
	err := unix.Statx(int(df.Fd()), filename,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME|unix.STATX_SIZE,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", filename, err)
	}

	return &File{
		Name:       filename,
		Size:       statx.Size,
		AgeMinutes: uint32(time.Since(time.Unix(int64(statx.Atime.Sec), int64(statx.Atime.Nsec))).Minutes()),
	}, nil
}
