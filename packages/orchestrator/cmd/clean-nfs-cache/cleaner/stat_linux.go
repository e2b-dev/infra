//go:build linux

package cleaner

import (
	"fmt"
	"path/filepath"

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

	return &Candidate{
		Parent:    nil, // not relevant here
		FullPath:  fullPath,
		Size:      statx.Size,
		ATimeUnix: statx.Atime.Sec,
		BTimeUnix: statx.Btime.Sec,
	}, nil
}

func (c *Cleaner) statInDir(dirPath string, filename string) (*File, error) {
	c.StatxC.Add(1)
	c.StatxInDirC.Add(1)
	var statx unix.Statx_t
	fullPath := filepath.Join(dirPath, filename)
	err := unix.Statx(unix.AT_FDCWD, fullPath,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME|unix.STATX_SIZE,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", fullPath, err)
	}

	return &File{
		Name:      filename,
		Size:      statx.Size,
		ATimeUnix: statx.Atime.Sec,
	}, nil
}
