//go:build linux

package cleaner

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (c *Cleaner) stat(fullPath string) (*Stat, error) {
	c.StatxC.Add(1)
	var statx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, fullPath,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME|unix.STATX_BTIME,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", fullPath, err)
	}

	return &Stat{
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
// Safety: the caller must keep df open until this returns. sampleDataDir enforces
// that by draining all in-flight stat responses before it closes df.
func (c *Cleaner) statInDir(df *os.File, filename string) (*Stat, error) {
	c.StatxC.Add(1)
	var statx unix.Statx_t
	err := unix.Statx(int(df.Fd()), filename,
		unix.AT_STATX_DONT_SYNC|unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_ATIME,
		&statx,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to statx %q: %w", filepath.Join(df.Name(), filename), err)
	}

	return &Stat{
		ATimeUnix: statx.Atime.Sec,
	}, nil
}
