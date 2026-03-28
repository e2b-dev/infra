package cgroup

import "context"

// noopManager is a cgroup Manager that does not create real cgroups.
// Used in environments where cgroup accounting is not needed (CLI tools, tests).
type noopManager struct{}

var _ Manager = (*noopManager)(nil)

// NewNoopManager returns a Manager that creates noop cgroup handles.
// The handles are safe to use but do not perform any actual cgroup operations.
func NewNoopManager() Manager {
	return &noopManager{}
}

func (m *noopManager) Initialize(_ context.Context) error {
	return nil
}

func (m *noopManager) Create(_ context.Context, cgroupName string) (*CgroupHandle, error) {
	return newNoopHandle(cgroupName), nil
}

// newNoopHandle creates a CgroupHandle that performs no real cgroup operations.
// GetFD returns NoCgroupFD, GetStats returns (nil, nil), Remove is a no-op.
func newNoopHandle(cgroupName string) *CgroupHandle {
	return &CgroupHandle{
		cgroupName: cgroupName,
		noop:       true,
	}
}
