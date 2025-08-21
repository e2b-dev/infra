//go:build darwin

package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func getMetadata(path string) (file, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return file{}, fmt.Errorf("could not stat info: %w", err)
	}

	actualStruct, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return file{}, fmt.Errorf("stat did not return a syscall.Stat_t for %q: %T",
			stat.Name(), stat.Sys())
	}

	return file{
		path:  stat.Name(),
		size:  stat.Size(),
		atime: fromTimespec(actualStruct.Atimespec),
		btime: fromTimespec(actualStruct.Birthtimespec),
	}, nil
}

func fromTimespec(ts syscall.Timespec) time.Time {
	return time.Unix(ts.Sec, ts.Nsec)
}
