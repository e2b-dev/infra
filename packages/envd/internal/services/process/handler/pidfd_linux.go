//go:build linux

package handler

import "golang.org/x/sys/unix"

// pidfdOpen returns a pidfd referring to pid via pidfd_open(2). Linux-only; the
// live-upgrade reaper uses it to wait on a re-adopted process it did not fork.
func pidfdOpen(pid int) (int, error) {
	return unix.PidfdOpen(pid, 0)
}
