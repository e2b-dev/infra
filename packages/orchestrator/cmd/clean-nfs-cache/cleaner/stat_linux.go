//go:build linux

package cleaner

import (
	"fmt"
	"os"
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

// statInDir issues an fd-relative statx against an already-open directory fd.
// On NFS this is dramatically cheaper than statx(AT_FDCWD, abs_path), because
// the open dir's file handle is cached on the server and a single GETATTR RPC
// satisfies the call; AT_FDCWD with an absolute path forces a LOOKUP chain
// for every uncached path component.
//
// Safety: the caller must keep df open until this returns. scanDir enforces
// that by draining all in-flight stat responses before its deferred Close.
func (c *Cleaner) statInDir(df *os.File, filename string) (*File, error) {
	c.StatxC.Add(1)
	c.StatxInDirC.Add(1)
	var statx unix.Statx_t
	err := unix.Statx(int(df.Fd()), filename,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME|unix.STATX_SIZE,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", filepath.Join(df.Name(), filename), err)
	}

	return &File{
		Name:      filename,
		Size:      statx.Size,
		ATimeUnix: statx.Atime.Sec,
	}, nil
}
