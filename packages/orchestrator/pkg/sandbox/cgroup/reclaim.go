package cgroup

import (
	"context"
	"errors"
	"fmt"
)

// ReclaimLeaked destroys every sandbox cgroup under root left over from a
// previous orchestrator run, killing any remaining processes in them. It returns
// the number of cgroups destroyed and any failures.
func ReclaimLeaked(ctx context.Context, manager Manager, root string) (int, []error) {
	if manager == nil {
		return 0, []error{errors.New("cgroup manager is nil")}
	}

	names, err := ListSandboxCgroups(root)
	if err != nil {
		return 0, []error{err}
	}

	reclaimed := 0
	var failures []error
	for _, name := range names {
		if err := manager.Destroy(ctx, name); err != nil {
			failures = append(failures, fmt.Errorf("failed to remove cgroup %s: %w", name, err))

			continue
		}

		reclaimed++
	}

	return reclaimed, failures
}
