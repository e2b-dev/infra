//go:build linux

package main

import (
	"testing"
)

func TestOptionsValidate(t *testing.T) {
	valid := options{
		mountNSPath:    "/run/fc-mntns/mnt-test",
		netNSPath:      "/var/run/netns/ns-test",
		rootfsMountDir: "/fc-vm",
		rootfsSource:   "/orchestrator/sandbox/rootfs.link",
		rootfsLink:     "/fc-vm/rootfs.ext4",
		kernelDir:      "/fc-vm/kernel",
		kernelSource:   "/fc-kernels/6.1/vmlinux.bin",
		kernelLink:     "/fc-vm/kernel/vmlinux.bin",
		firecracker:    "/fc-versions/v1/firecracker",
		apiSock:        "/orchestrator/sandbox/fc.sock",
	}

	if err := valid.validate(); err != nil {
		t.Fatalf("valid options failed validation: %v", err)
	}

	missingFirecracker := valid
	missingFirecracker.firecracker = ""
	if err := missingFirecracker.validate(); err == nil {
		t.Fatal("expected missing firecracker path to fail validation")
	}
}
