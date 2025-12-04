package handler

import (
	"errors"
	"fmt"
	"os"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup2"
)

var ErrCGroup2Unsupported = errors.New("unsupported cgroup v2")

type CGroupManager struct {
	mgr *cgroup2.Manager
}

func NewCGroupManager(parent, name string, resources *cgroup2.Resources) *CGroupManager {
	switch mode := cgroups.Mode(); mode {
	case cgroups.Unified:
	default:
		fmt.Fprintf(os.Stderr, "unsupported cgroups mode: %v\n", mode)

		return nil
	}

	parentSlice, err := cgroup2.LoadSystemd(parent, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load parent cgroups %q: %v", parent, err)

		return nil
	}

	childSlice, err := parentSlice.NewChild(name, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create child cgroup %q: %v", name, err)
		return nil
	}

	return &CGroupManager{mgr: childSlice}
}

func (m *CGroupManager) Assign(pid int) error {
	if m == nil {
		return fmt.Errorf("failed to assign cgroup to pid: cgroup unsupported")
	}

	err := m.mgr.AddProc(uint64(pid))
	if err != nil {
		return fmt.Errorf("failed to add process to cgroup: %w", err)
	}

	return nil
}
