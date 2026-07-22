package cgroups

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
)

// WorkloadProcessTypes are the cgroups holding the customer workload: the
// processes/shells envd spawns (user) and PTY sessions (ptys). These are frozen
// before a pause and thawed on resume; envd's own system processes are excluded.
var WorkloadProcessTypes = []ProcessType{ProcessTypeUser, ProcessTypePTY}

// WorkloadFreezer serializes freeze/unfreeze of the workload cgroups across
// every caller — the pre-pause /freeze, the pause-rollback /unfreeze, the /init
// deferred resume-thaw, and the live-upgrade handover — through a single lock,
// so their per-cgroup sweeps can never interleave and strand the workload
// frozen. Freeze and Unfreeze are best-effort: each attempts every cgroup even
// if one fails and returns the joined error.
//
// A single WorkloadFreezer instance must be shared by all of those callers for
// the serialization to hold; construct one and pass it to each.
type WorkloadFreezer struct {
	mgr  Manager
	lock *semaphore.Weighted
}

// NewWorkloadFreezer wraps a cgroup manager with the shared freeze lock.
func NewWorkloadFreezer(mgr Manager) *WorkloadFreezer {
	return &WorkloadFreezer{mgr: mgr, lock: semaphore.NewWeighted(1)}
}

// Manager returns the underlying cgroup manager, for callers that also need it
// for non-freeze work such as process placement.
func (f *WorkloadFreezer) Manager() Manager { return f.mgr }

// Freeze freezes the workload cgroups, serialized against all other callers. The
// ctx bounds only the wait for the lock.
func (f *WorkloadFreezer) Freeze(ctx context.Context) error {
	release, err := f.FreezeHold(ctx)
	release()

	return err
}

// FreezeHold freezes the workload cgroups and KEEPS the lock held, returning a
// release func. Unlike Freeze (which releases as soon as the sweep is done), this
// lets a caller keep the freeze uninterruptible across a critical section — the
// live-upgrade handover — so a concurrent Unfreeze (e.g. /init's deferred
// resume-thaw or /unfreeze) blocks on the lock until release is called and cannot
// thaw the workload mid-handover. The frozen cgroup state persists after release;
// release only drops the lock and is idempotent. On a lock-acquire failure it
// returns a no-op release and the error.
func (f *WorkloadFreezer) FreezeHold(ctx context.Context) (release func(), err error) {
	if err := f.lock.Acquire(ctx, 1); err != nil {
		return func() {}, err
	}

	var once sync.Once
	release = func() { once.Do(func() { f.lock.Release(1) }) }

	var errs []error
	for _, pt := range WorkloadProcessTypes {
		if e := f.mgr.Freeze(pt); e != nil {
			errs = append(errs, fmt.Errorf("freeze %s cgroup: %w", pt, e))
		}
	}

	return release, errors.Join(errs...)
}

// Unfreeze thaws the workload cgroups, serialized against all other callers. It
// detaches the lock wait from ctx cancellation so the thaw always lands even if
// the caller's request context is cancelled — a dropped unfreeze would strand
// the workload frozen. Thawing a non-frozen cgroup is a no-op, so it is safe to
// call unconditionally on every upgrade/resume outcome.
func (f *WorkloadFreezer) Unfreeze(ctx context.Context) error {
	if err := f.lock.Acquire(context.WithoutCancel(ctx), 1); err != nil {
		return err
	}
	defer f.lock.Release(1)

	var errs []error
	for _, pt := range WorkloadProcessTypes {
		if err := f.mgr.Unfreeze(pt); err != nil {
			errs = append(errs, fmt.Errorf("unfreeze %s cgroup: %w", pt, err))
		}
	}

	return errors.Join(errs...)
}
