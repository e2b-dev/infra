//go:build linux

package pkg

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func GetFileMetadata(fullPath string) (File, error) {
	var statx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, fullPath,
		unix.AT_STATX_SYNC_AS_STAT,
		unix.STATX_ATIME|unix.STATX_MTIME|unix.STATX_CTIME|unix.STATX_BTIME,
		&statx,
	)
	if err != nil {
		return File{}, fmt.Errorf("failed to statx %q: %w", fullPath, err)
	}

	var t File
	t.Path = fullPath
	t.Size = int64(statx.Size)
	t.ATime = statxTimestampToTime(statx.Atime)

	if statx.Mask&unix.STATX_BTIME == unix.STATX_BTIME {
		t.BTime = statxTimestampToTime(statx.Btime)
	}

	return t, nil
}

func statxTimestampToTime(ts unix.StatxTimestamp) time.Time {
	return time.Unix(ts.Sec, int64(ts.Nsec))
}
