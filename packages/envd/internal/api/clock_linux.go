//go:build linux

package api

import (
	"time"

	"golang.org/x/sys/unix"
)

func setSystemTime(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())

	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}
