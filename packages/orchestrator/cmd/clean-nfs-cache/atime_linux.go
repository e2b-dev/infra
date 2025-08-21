//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"time"

	"golang.org/x/sys/unix"
)

func getAtime(info fs.FileInfo) (time.Time, error) {
	hasAtime, ok := info.Sys().(*unix.Stat_t)
	if !ok {
		return time.Time{}, fmt.Errorf("could not stat unix stat_t for %q: %T", info.Name(), info.Sys())
	}

	t := hasAtime.Atim
	return time.Unix(t.Sec, t.Nsec), nil
}
