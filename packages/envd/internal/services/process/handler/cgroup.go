package handler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup2"
	"golang.org/x/sys/unix"
)

type CGroupManager struct {
	cgroupFD  int
	mgr       *cgroup2.Manager
	closeOnce sync.Once
}

type Limits struct {
	MaxMemory int64
}

func NewCGroupManager(slice, group string, resources *cgroup2.Resources) *CGroupManager {
	if strings.Contains(slice, "/") || strings.Contains(group, "/") {
		fmt.Fprintf(os.Stderr, "cgroup slice (%q) or group (%s) contains invalid characters\n", slice, group)

		return nil
	}

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
	mgr, err := cgroup2.LoadSystemd(slice, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create cgroup manager: %v", err)

		return nil
	}

	mgr, err = mgr.NewChild(group, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create cgroup: %v", err)

		return nil
	}

	fd, err := unix.Open(sysfsPath, 0, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open cgroup %q: %v", sysfsPath, err)

		if err := mgr.DeleteSystemd(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to delete cgroup %q, %q: %v", slice, group, err)
		}

		return nil
	}

	return &CGroupManager{cgroupFD: fd, mgr: mgr}
}

func (m *CGroupManager) enabled() bool {
	return m != nil
}

func (m *CGroupManager) GetFileDescriptor() (int, bool) {
	if !m.enabled() {
		return 0, false
	}

	return m.cgroupFD, true
}

func (m *CGroupManager) Close() error {
	var err error
	m.closeOnce.Do(func() {
		if !m.enabled() {
			return
		}

		var errs []error
		if err := unix.Close(m.cgroupFD); err != nil {
			errs = append(errs, fmt.Errorf("failed to close cgroup fd: %w", err))
		}

		if err := m.mgr.DeleteSystemd(); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete cgroup: %w", err))
		}

		err = errors.Join(errs...)
	})

	return err
}
