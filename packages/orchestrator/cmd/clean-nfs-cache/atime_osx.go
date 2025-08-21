//go:build darwin

package main

import (
	"fmt"
	"io/fs"
	"syscall"
	"time"
)

func getAtime(info fs.FileInfo) (time.Time, error) {
	hasAtime, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, fmt.Errorf("could not stat unix stat_t for %q: %T", info.Name(), info.Sys())
	}

	t := hasAtime.Atimespec
	return time.Unix(t.Sec, t.Nsec), nil
}
