package cgroups

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// WorkloadProcessTypes are the cgroups holding the customer workload: the
// processes/shells envd spawns (user) and PTY sessions (ptys). These are frozen
// before a pause and thawed on resume; envd's own system processes are excluded.
var WorkloadProcessTypes = []ProcessType{ProcessTypeUser, ProcessTypePTY}

// freezePollInterval is how often cgroup.events is re-read while waiting for a
// freeze to land. Short enough that a fast freeze is not rounded up to something
// visible on the pause path, long enough not to spin.
const freezePollInterval = 2 * time.Millisecond

// HandoverMaxWait bounds the live-upgrade wait, where the policy is strict: the swap is
// refused rather than execve over a running workload. It stays modest because the
// fallback is cheap (keep the current envd) and a slow handover holds the freeze lock,
// blocking the resume thaw.
//
// The pre-pause wait has no constant here on purpose: that budget belongs entirely to
// the orchestrator, which sends it as maxWaitMs. It owns the call timeout, so only it
// knows what wait is observable, and a default of ours would silently compete with it.
const HandoverMaxWait = 2 * time.Second

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

	// thawMu guards thawedCh, the channel closed on the next Unfreeze. It lets
	// callers block until the workload is next thawed (see Thawed).
	thawMu   sync.Mutex
	thawedCh chan struct{}
}

// NewWorkloadFreezer wraps a cgroup manager with the shared freeze lock.
func NewWorkloadFreezer(mgr Manager) *WorkloadFreezer {
	return &WorkloadFreezer{mgr: mgr, lock: semaphore.NewWeighted(1)}
}

// Thawed returns a channel that is closed the next time the workload is
// unfrozen (a fresh one is installed after each Unfreeze). A re-adopted
// process's carried kill-timer selects on it so the timeout only starts once the
// workload actually runs — the new envd runs while the re-adopted workload is
// still frozen (until the post-upgrade /init), and the timeout must measure
// running time, not frozen time. Call it while the workload is frozen (before
// the thaw you want to observe) so the close isn't missed.
func (f *WorkloadFreezer) Thawed() <-chan struct{} {
	f.thawMu.Lock()
	defer f.thawMu.Unlock()
	if f.thawedCh == nil {
		f.thawedCh = make(chan struct{})
	}

	return f.thawedCh
}

// signalThawed closes the pending Thawed channel (if any) to wake waiters.
func (f *WorkloadFreezer) signalThawed() {
	f.thawMu.Lock()
	defer f.thawMu.Unlock()
	if f.thawedCh != nil {
		close(f.thawedCh)
		f.thawedCh = nil
	}
}

// Manager returns the underlying cgroup manager, for callers that also need it
// for non-freeze work such as process placement.
func (f *WorkloadFreezer) Manager() Manager { return f.mgr }

// FreezeResult reports what a freeze achieved, separating what we DID from what we
// OBSERVED.
//
// Frozen+NotFrozen+Unobservable accounts for every cgroup whose state we managed to read,
// and those are a subset of Requested. Failed does NOT reconcile against Requested,
// because it spans both phases: a write that never landed (so the cgroup was never
// Requested at all) and a state read that errored (so it was). The totals therefore are
//
//	Requested + <writes that failed> == cgroups attempted
//	Frozen + NotFrozen + Unobservable + <reads that failed> == Requested
//
// with Failed being the sum of the two failure terms. Splitting Failed in two would make
// both lines add up on their own; it is kept as one count because a caller acting on it
// does the same thing either way -- tolerate and carry on.
//
// Note that Frozen is a state read back from cgroup.events, not an acknowledgement of
// our write -- a cgroup the guest froze itself reads frozen too. We never establish
// that our write caused it, only that the workload is stopped, which is the property
// the snapshot actually depends on.
type FreezeResult struct {
	// Requested is the number of cgroups this call wrote cgroup.freeze to.
	Requested int
	// Frozen read back "frozen 1" from cgroup.events within the budget: their tasks
	// have stopped, so a snapshot taken now captures them stopped.
	Frozen int
	// NotFrozen still read "frozen 0" when the budget expired. Their tasks may still
	// be running, so a snapshot taken now can capture a live workload.
	NotFrozen int
	// Failed is the number of cgroups whose write or read errored. Expected and
	// tolerated: a threaded cgroup rejects cgroup.freeze, and a cgroup removed
	// mid-sweep reports ENOENT.
	Failed int
	// Unobservable is the number of cgroups whose freeze state cannot be read at all,
	// because this guest has no cgroup manager. Neither a success nor a failure: the
	// write was accepted and there is simply nothing to read back, so these are held
	// apart from NotFrozen rather than reported as a workload that refused to stop.
	Unobservable int
	// SweepDuration is the time spent issuing the writes -- our own cost, scaling
	// with the number of cgroups.
	SweepDuration time.Duration
	// WaitDuration is the time spent polling cgroup.events -- the guest's cost,
	// scaling with how deep in I/O its tasks were. Kept separate from SweepDuration
	// because the two have different causes and different fixes. It is outcome
	// neutral: the wait ends either because everything stopped or because the budget
	// ran out, and this records how long it took either way.
	WaitDuration time.Duration
}

// AllFrozen reports whether every cgroup this call wrote to was then read back frozen.
// Unobservable is not counted against it: a guest with no cgroup manager never had this
// guarantee, and withholding it would newly block the live-upgrade handover there rather
// than protect anything.
func (r FreezeResult) AllFrozen() bool { return r.NotFrozen == 0 && r.Failed == 0 }

// Freeze freezes the workload cgroups and waits for them to read back frozen,
// serialized against all other callers. The ctx bounds both the wait for the lock and
// the wait for the state: a caller that has gone away must not leave us polling, because
// the poll holds the freeze lock and would delay the rollback thaw behind it.
func (f *WorkloadFreezer) Freeze(ctx context.Context, maxWait time.Duration) (FreezeResult, error) {
	release, res, err := f.FreezeHold(ctx, maxWait)
	release()

	return res, err
}

// FreezeHold freezes the workload cgroups and KEEPS the lock held, returning a
// release func. Unlike Freeze (which releases as soon as the sweep is done), this
// lets a caller keep the freeze uninterruptible across a critical section — the
// live-upgrade handover — so a concurrent Unfreeze (e.g. /init's deferred
// resume-thaw or /unfreeze) blocks on the lock until release is called and cannot
// thaw the workload mid-handover. The frozen cgroup state persists after release;
// release only drops the lock and is idempotent. On a lock-acquire failure it
// returns a no-op release and the error.
func (f *WorkloadFreezer) FreezeHold(ctx context.Context, maxWait time.Duration) (release func(), res FreezeResult, err error) {
	if err := f.lock.Acquire(ctx, 1); err != nil {
		return func() {}, FreezeResult{}, err
	}

	var once sync.Once
	release = func() { once.Do(func() { f.lock.Release(1) }) }

	var errs []error
	sweepStart := time.Now()
	pending := make([]ProcessType, 0, len(WorkloadProcessTypes))
	for _, pt := range WorkloadProcessTypes {
		if e := f.mgr.Freeze(pt); e != nil {
			errs = append(errs, fmt.Errorf("freeze %s cgroup: %w", pt, e))
			res.Failed++

			continue
		}
		res.Requested++
		pending = append(pending, pt)
	}
	res.SweepDuration = time.Since(sweepStart)

	frozen, failed, unobservable, waitErrs := f.awaitFrozen(ctx, pending, maxWait)
	res.Frozen = frozen
	res.Unobservable = unobservable
	res.NotFrozen = res.Requested - frozen - failed - unobservable
	res.Failed += failed
	res.WaitDuration = time.Since(sweepStart) - res.SweepDuration
	errs = append(errs, waitErrs...)

	return release, res, errors.Join(errs...)
}

// awaitFrozen polls the given cgroups until each reports frozen, the budget expires, or
// ctx is cancelled. All cgroups are polled together rather than one after another: the wait
// is then bounded by the slowest cgroup instead of by the sum, which matters because
// a single busy cgroup has been measured taking seconds to stop while the whole
// pre-pause freeze budget is of that order.
func (f *WorkloadFreezer) awaitFrozen(ctx context.Context, pts []ProcessType, budget time.Duration) (frozen, failed, unobservable int, errs []error) {
	if len(pts) == 0 || budget <= 0 {
		return 0, 0, 0, nil
	}

	remaining := slices.Clone(pts)
	deadline := time.Now().Add(budget)

	for {
		next := remaining[:0]
		for _, pt := range remaining {
			isFrozen, err := f.mgr.Frozen(pt)
			switch {
			case errors.Is(err, ErrFrozenUnobservable):
				// Nothing to read and nothing to wait for. Drop it from the poll set so
				// the loop exits immediately instead of spinning out the whole budget.
				unobservable++
			case err != nil:
				failed++
				errs = append(errs, fmt.Errorf("read %s cgroup.events: %w", pt, err))
			case isFrozen:
				frozen++
			default:
				next = append(next, pt)
			}
		}
		remaining = next

		// Cancellation is checked alongside the deadline: the caller owns the budget, so
		// once it stops listening the rest of that budget is ours to give back rather
		// than spend holding the lock.
		if len(remaining) == 0 || !time.Now().Before(deadline) || ctx.Err() != nil {
			return frozen, failed, unobservable, errs
		}

		sleep := min(freezePollInterval, time.Until(deadline))
		if sleep > 0 {
			select {
			case <-ctx.Done():
				return frozen, failed, unobservable, errs
			case <-time.After(sleep):
			}
		}
	}
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

	// Wake anyone waiting for the workload to run again (carried kill-timers).
	f.signalThawed()

	return errors.Join(errs...)
}
