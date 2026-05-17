//go:build linux

package handler

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func applyAmbientCapSysNice(attr *syscall.SysProcAttr) {
	attr.AmbientCaps = append(attr.AmbientCaps, unix.CAP_SYS_NICE)
}
