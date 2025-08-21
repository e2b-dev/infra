//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

func getMetadata(info fs.DirEntry) (file, error) {
	actualStruct, err := statX(info.Name())
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to stat: %w", info.Name(), err)
	}

	return file{
		path:  info.Name(),
		size:  actualStruct.Size(),
		atime: actualStruct.AccessTime(),
		btime: actualStruct.BirthTime(),
	}, nil
}

// everything below is copied from github.com/djherbis/times.
// they don't expose size, which we need.
var (
	supportsStatx int32 = 1
	statxFunc           = unix.Statx
)

func isStatXSupported() bool {
	return atomic.LoadInt32(&supportsStatx) == 1
}

func statX(name string) (Timespec, error) {
	// https://man7.org/linux/man-pages/man2/statx.2.html
	var statx unix.Statx_t
	err := statxFunc(unix.AT_FDCWD, name, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_ATIME|unix.STATX_MTIME|unix.STATX_CTIME|unix.STATX_BTIME, &statx)
	if err != nil {
		return nil, err
	}
	return extractTimes(&statx), nil
}

func extractTimes(statx *unix.Statx_t) file {
	var t file
	t.path = statx.Name
	t.size = statx.Size
	t.atime.v = statxTimestampToTime(statx.Atime)

	if statx.Mask&unix.STATX_BTIME == unix.STATX_BTIME {
		t.btime.v = statxTimestampToTime(statx.Btime)
	}
	return t
}

func statxTimestampToTime(ts unix.StatxTimestamp) time.Time {
	return time.Unix(ts.Sec, int64(ts.Nsec))
}
