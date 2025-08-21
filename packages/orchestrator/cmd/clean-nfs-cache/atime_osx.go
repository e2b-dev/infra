//go:build darwin

package main

import (
	"fmt"
	"io/fs"
	"syscall"
	"time"
)

func getAtime(info fs.FileInfo) (time.Time, time.Time, error) {
	actualStruct, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, time.Time{}, fmt.Errorf("could not stat unix stat_t for %q: %T", info.Name(), info.Sys())
	}

	return fromTimespec(actualStruct.Atimespec), fromTimespec(actualStruct.Birthtimespec), nil
}

func fromTimespec(ts syscall.Timespec) time.Time {
	return time.Unix(ts.Sec, ts.Nsec)
}
