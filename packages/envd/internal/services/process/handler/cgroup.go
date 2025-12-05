package handler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/cgroups/v3"
	"golang.org/x/sys/unix"
)

var ErrCGroup2Unsupported = errors.New("unsupported cgroup v2")

type CGroupManager struct {
	cgroupFD int
}

const defaultDirPerm = 0o755

type Limits struct {
	MaxMemory int64
}

func NewCGroupManager(sysfsPath string, fields map[string]string) *CGroupManager {
	switch mode := cgroups.Mode(); mode {
	case cgroups.Unified:
	default:
		fmt.Fprintf(os.Stderr, "unsupported cgroups mode: %v\n", mode)

		return nil
	}

	if err := os.MkdirAll(sysfsPath, defaultDirPerm); err != nil {
		fmt.Fprintf(os.Stderr, "failed to make cgroup %q: %v", sysfsPath, err)

		return nil
	}

	for prop, value := range fields {
		propPath := filepath.Join(sysfsPath, prop)
		err := os.WriteFile(propPath, []byte(value), defaultDirPerm)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to write cgroup property %q: %v", propPath, err)
		}
	}

	fd, err := unix.Open(sysfsPath, 0, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open cgroup %q: %v", sysfsPath, err)

		return nil
	}

	return &CGroupManager{cgroupFD: fd}
}

func (m *CGroupManager) enabled() bool {
	return m != nil && m.cgroupFD != 0
}

func (m *CGroupManager) GetFileDescriptor() (int, bool) {
	if !m.enabled() {
		return 0, false
	}

	return m.cgroupFD, true
}

func (m *CGroupManager) Close() error {
	if !m.enabled() {
		return nil
	}

	return unix.Close(m.cgroupFD)
}
