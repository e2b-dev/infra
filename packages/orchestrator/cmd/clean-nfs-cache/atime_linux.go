//go:build linux

package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func getMetadata(fullPath string) (file, error) {
	var statx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, fullPath,
		unix.AT_STATX_SYNC_AS_STAT,
		unix.STATX_ATIME|unix.STATX_MTIME|unix.STATX_CTIME|unix.STATX_BTIME,
		&statx,
	)
	if err != nil {
		return file{}, fmt.Errorf("failed to statx %q: %w", fullPath, err)
	}

	var t file
	t.path = fullPath
	t.size = int64(statx.Size)
	t.atime = statxTimestampToTime(statx.Atime)

	if statx.Mask&unix.STATX_BTIME == unix.STATX_BTIME {
		t.btime = statxTimestampToTime(statx.Btime)
	}
	return t, nil
}

func statxTimestampToTime(ts unix.StatxTimestamp) time.Time {
	return time.Unix(ts.Sec, int64(ts.Nsec))
}
