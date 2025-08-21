//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"time"

	"golang.org/x/sys/unix"
)

func getAtime(info fs.FileInfo) (time.Time, time.Time, error) {
	actualStruct, ok := info.Sys().(*unix.Stat_t)
	if !ok {
		return time.Time{}, fmt.Errorf("could not stat unix stat_t for %q: %T", info.Name(), info.Sys())
	}

	return fromTimespec(actualStruct.Atim), fromTimespec(actualStruct.Btim), nil
}

func fromTimespec(ts unix.Timespec) time.Time {
	return time.Unix(int64(ts.Sec), int64(ts.Nsec))
}
