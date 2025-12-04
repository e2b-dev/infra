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

func NewCGroupManager() *CGroupManager {
	switch mode := cgroups.Mode(); mode {
	case cgroups.Unified:
	default:
		fmt.Fprintf(os.Stderr, "unsupported cgroups mode: %v\n", mode)

		return nil
	}

	m, err := cgroup2.LoadSystemd("/", "envd.slice")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load systemd cgroups: %v", err)

		return nil
	}

	res := cgroup2.Resources{}
	m, err = m.NewChild("envd-commands.slice", &res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create systemd sub-cgroup: %v", err)
	}

	return &CGroupManager{mgr: m}
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
