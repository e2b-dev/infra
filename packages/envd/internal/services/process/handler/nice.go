package handler

import "syscall"

// resetNice resets the nice value of a process to the default level (0).
// This is needed because envd runs with Nice=-20 (set in the systemd unit),
// and child processes inherit this priority. User processes should run at
// normal priority.
func resetNice(pid int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, 0)
}
