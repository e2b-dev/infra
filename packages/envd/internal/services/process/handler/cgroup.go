package handler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup2"
	"golang.org/x/sys/unix"
)

var ErrCGroup2Unsupported = errors.New("unsupported cgroup v2")

type CGroupManager struct {
	cgroupFD int
	mgr      *cgroup2.Manager
}

const defaultDirPerm = 0o755

type Limits struct {
	MaxMemory int64
}

func NewCGroupManager(slice, group string, resources *cgroup2.Resources) *CGroupManager {
	if slice == "" || group == "" {
		fmt.Fprintf(os.Stderr, "cgroup slice or group not set\n")
		return nil
	}

	switch mode := cgroups.Mode(); mode {
	case cgroups.Unified:
	default:
		fmt.Fprintf(os.Stderr, "unsupported cgroups mode: %v\n", mode)

		return nil
	}

	sysfsPath := filepath.Join("/sys/fs/cgroup", slice, group)
	mgr, err := cgroup2.NewSystemd(slice, group, -1, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create cgroup manager: %v", err)
		return nil
	}

	fd, err := unix.Open(sysfsPath, 0, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open cgroup %q: %v", sysfsPath, err)

		return nil
	}

	return &CGroupManager{cgroupFD: fd, mgr: mgr}
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

	var errs []error
	if err := unix.Close(m.cgroupFD); err != nil {
		errs = append(errs, fmt.Errorf("failed to close cgroup fd: %w", err))
	}

	if err := m.mgr.DeleteSystemd(); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete cgroup: %w", err))
	}

	return errors.Join(errs...)
}
