//go:build linux

package network

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseMountNamespaceKeepPaths(t *testing.T) {
	got := parseMountNamespaceKeepPaths(" /proc,dev,/proc,/orchestrator/cache ")
	want := []string{"/", "/proc", "/dev", "/orchestrator/cache"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected keep paths: got %#v, want %#v", got, want)
	}
}

func TestNewPoolKeepsHomeMountByDefault(t *testing.T) {
	pool := NewPool(2, 2, &fakeStorage{}, Config{})

	if !shouldKeepMountPoint("/home", pool.mountNamespaces.keepPaths) {
		t.Fatal("default mount namespace template keep paths must keep /home")
	}
}

func TestParseMountInfoMountPoints(t *testing.T) {
	data := "24 0 8:1 / / rw,relatime - ext4 /dev/root rw\n" +
		"25 24 0:22 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw\n" +
		"26 24 0:23 / /mnt/space\\040dir rw,relatime - tmpfs tmpfs rw\n"

	got, err := parseMountInfoMountPoints(data)
	if err != nil {
		t.Fatalf("parse mountinfo: %v", err)
	}

	want := []string{"/", "/proc", "/mnt/space dir"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected mount points: got %#v, want %#v", got, want)
	}
}

func TestShouldKeepMountPoint(t *testing.T) {
	keepPaths := []string{"/", "/proc", "/dev", "/orchestrator/cache/rootfs"}

	cases := []struct {
		name       string
		mountPoint string
		want       bool
	}{
		{name: "exact keep", mountPoint: "/proc", want: true},
		{name: "ancestor of keep path", mountPoint: "/orchestrator", want: true},
		{name: "child mount under kept path is pruned", mountPoint: "/proc/sys", want: false},
		{name: "sibling is pruned", mountPoint: "/orchestrator/logs", want: false},
		{name: "unrelated is pruned", mountPoint: "/var/lib/kubelet/pods/abc", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldKeepMountPoint(tc.mountPoint, keepPaths)
			if got != tc.want {
				t.Fatalf("shouldKeepMountPoint(%q) = %t, want %t", tc.mountPoint, got, tc.want)
			}
		})
	}
}

func TestSlotMountNamespaceLifecycle(t *testing.T) {
	slot := &Slot{Idx: 42}
	tmpDir := t.TempDir()
	mountNS := &mountNamespace{
		Name:    "test",
		Path:    filepath.Join(tmpDir, "mntns"),
		PIDPath: filepath.Join(tmpDir, "mntns.pid"),
	}

	if err := slot.assignMountNamespace(nil); err == nil {
		t.Fatal("expected nil mount namespace assignment to fail")
	}

	if err := slot.assignMountNamespace(mountNS); err != nil {
		t.Fatalf("assign mount namespace: %v", err)
	}

	if err := slot.assignMountNamespace(mountNS); err == nil {
		t.Fatal("expected duplicate mount namespace assignment to fail")
	}

	path, ok := slot.AssignedMountNamespacePath()
	if !ok || path != mountNS.Path {
		t.Fatalf("unexpected assigned mount namespace path: path=%q ok=%t", path, ok)
	}

	if err := slot.releaseMountNamespace(); err != nil {
		t.Fatalf("release mount namespace: %v", err)
	}

	if _, ok := slot.AssignedMountNamespacePath(); ok {
		t.Fatal("mount namespace should be cleared after release")
	}

	if err := slot.releaseMountNamespace(); err != nil {
		t.Fatalf("second release should be a no-op: %v", err)
	}
}
