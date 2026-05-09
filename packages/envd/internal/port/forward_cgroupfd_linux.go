//go:build linux

package port

import "syscall"

// applyCgroupFD sets cgroup-related fields on Linux SysProcAttr.
func applyCgroupFD(attr *syscall.SysProcAttr, fd int, use bool) {
	attr.CgroupFD = fd
	attr.UseCgroupFD = use
}
