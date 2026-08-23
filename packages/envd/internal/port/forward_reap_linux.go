//go:build linux

package port

import "syscall"

// reapAdoptedSocat blocks until the re-adopted socat pid exits, then reaps it so
// it does not linger as a zombie under the same-PID envd. A socat spawned by
// this envd is reaped by its os/exec Wait goroutine, but a socat re-adopted
// across a live-upgrade has no such goroutine (the old runtime was replaced by
// execve), so we wait4 its pid directly. The pid is a child of this process (the
// PID is unchanged across the upgrade), so wait4 succeeds; it targets one
// specific pid, so it never steals os/exec's other children.
func reapAdoptedSocat(pid int) {
	var ws syscall.WaitStatus
	for {
		if _, err := syscall.Wait4(pid, &ws, 0, nil); err != syscall.EINTR {
			return
		}
	}
}
