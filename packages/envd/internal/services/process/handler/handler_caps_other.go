//go:build !linux

package handler

import "syscall"

func applyAmbientCapSysNice(_ *syscall.SysProcAttr) {}
