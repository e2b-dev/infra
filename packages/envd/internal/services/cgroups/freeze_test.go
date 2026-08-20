package cgroups

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkloadFreezer_FreezeHoldBlocksUnfreeze verifies FreezeHold keeps the
// shared lock held so a concurrent Unfreeze cannot thaw the workload until
// release is called — the serialization the live-upgrade handover relies on.
func TestWorkloadFreezer_FreezeHoldBlocksUnfreeze(t *testing.T) {
	t.Parallel()

	f := NewWorkloadFreezer(NewNoopManager())

	release, _, err := f.FreezeHold(t.Context(), 0)
	require.NoError(t, err)

	unfrozen := make(chan struct{})
	go func() {
		_ = f.Unfreeze(t.Context())
		close(unfrozen)
	}()

	select {
	case <-unfrozen:
		t.Fatal("Unfreeze thawed the workload while the freeze hold was active")
	case <-time.After(100 * time.Millisecond):
		// expected: blocked on the held lock
	}

	release()

	select {
	case <-unfrozen:
		// expected: proceeds once the hold is released
	case <-time.After(2 * time.Second):
		t.Fatal("Unfreeze did not proceed after the hold was released")
	}

	assert.NotPanics(t, release, "release must be idempotent")
}

// fakeFreezeManager drives the state-reading path: writes succeed, and whether a
// cgroup ever reports frozen is controlled per process type.
type fakeFreezeManager struct {
	mu        sync.Mutex
	frozen    map[ProcessType]bool
	frozenAt  map[ProcessType]int // reads before this one report frozen
	reads     map[ProcessType]int
	freezeErr map[ProcessType]error
	frozenErr map[ProcessType]error
	readOrder []ProcessType
}

func newFakeFreezeManager() *fakeFreezeManager {
	return &fakeFreezeManager{
		frozen:    map[ProcessType]bool{},
		frozenAt:  map[ProcessType]int{},
		reads:     map[ProcessType]int{},
		freezeErr: map[ProcessType]error{},
		frozenErr: map[ProcessType]error{},
	}
}

func (m *fakeFreezeManager) GetFileDescriptor(ProcessType) (int, bool) { return 0, false }
func (m *fakeFreezeManager) Close() error                              { return nil }

func (m *fakeFreezeManager) Freeze(pt ProcessType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.freezeErr[pt]; err != nil {
		return err
	}
	m.frozen[pt] = true

	return nil
}

func (m *fakeFreezeManager) Unfreeze(pt ProcessType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frozen[pt] = false

	return nil
}

func (m *fakeFreezeManager) Frozen(pt ProcessType) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.frozenErr[pt]; err != nil {
		return false, err
	}
	m.reads[pt]++
	// The ORDER matters, not only the count: polling every cgroup once per round and polling
	// one cgroup to completion before starting the next produce identical counts, and differ
	// only in how the reads interleave.
	m.readOrder = append(m.readOrder, pt)
	at, ok := m.frozenAt[pt]
	if !ok {
		return m.frozen[pt], nil
	}

	return m.frozen[pt] && m.reads[pt] >= at, nil
}

func TestFreeze_ReadsBackEveryCgroupFrozen(t *testing.T) {
	t.Parallel()
	f := NewWorkloadFreezer(newFakeFreezeManager())

	res, err := f.Freeze(t.Context(), time.Second)
	require.NoError(t, err)
	assert.Equal(t, len(WorkloadProcessTypes), res.Requested)
	assert.Equal(t, len(WorkloadProcessTypes), res.Frozen)
	assert.Zero(t, res.NotFrozen)
	assert.Zero(t, res.Failed)
	assert.True(t, res.AllFrozen())
}

// TestFreeze_UnobservableIsNeitherAwaitedNorCountedNotFrozen pins the noop/stub
// managers' contract. Reporting (false, nil) instead of the sentinel would make every
// freeze on a cgroup-less guest look like a workload refusing to stop: each pause would
// spin out the entire wait budget, and the strict live-upgrade handover would
// refuse every swap, on guests that never had the guarantee in the first place.
func TestFreeze_UnobservableIsNeitherAwaitedNorCountedNotFrozen(t *testing.T) {
	t.Parallel()
	f := NewWorkloadFreezer(NewNoopManager())

	// A budget far larger than the test's patience: if the wait were entered at all, the
	// elapsed assertion below would catch it.
	start := time.Now()
	res, err := f.Freeze(t.Context(), 10*time.Second)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, len(WorkloadProcessTypes), res.Requested)
	assert.Equal(t, len(WorkloadProcessTypes), res.Unobservable)
	assert.Zero(t, res.Frozen)
	assert.Zero(t, res.NotFrozen, "unobservable state is not a workload that refused to stop")
	assert.Zero(t, res.Failed, "nor is it a failure")
	assert.True(t, res.AllFrozen(),
		"AllFrozen must hold, or the handover would refuse every swap on such a guest")
	assert.Less(t, elapsed, time.Second, "must not poll for a state that can never appear")
}

func TestFreeze_ReportsNotFrozenRatherThanFailing(t *testing.T) {
	t.Parallel()
	mgr := newFakeFreezeManager()
	// This cgroup's tasks never reach a signal-delivery point, so cgroup.events never
	// reports frozen. The pause must still succeed: an unfreezable customer task must
	// never fail their pause.
	mgr.frozenAt[WorkloadProcessTypes[0]] = 1 << 30
	f := NewWorkloadFreezer(mgr)

	res, err := f.Freeze(t.Context(), 20*time.Millisecond)
	require.NoError(t, err, "a workload that will not stop is reported, not an error")
	assert.Equal(t, 1, res.NotFrozen)
	assert.Equal(t, len(WorkloadProcessTypes)-1, res.Frozen)
	assert.False(t, res.AllFrozen())
}

func TestFreeze_WaitIsBoundedBySlowestNotSum(t *testing.T) {
	t.Parallel()
	mgr := newFakeFreezeManager()
	// Every cgroup needs several reads before it reads frozen. Polled together the wait is
	// one slow cgroup's worth; polled one after another it would be the sum, which is
	// what would blow the pause budget on a guest with many busy cgroups.
	const readsBeforeFrozen = 5
	for _, pt := range WorkloadProcessTypes {
		mgr.frozenAt[pt] = readsBeforeFrozen
	}
	f := NewWorkloadFreezer(mgr)

	res, err := f.Freeze(t.Context(), 5*time.Second)

	require.NoError(t, err)
	require.True(t, res.AllFrozen())

	// EVERY round must name every cgroup, not just the first: a sweep of everything followed by
	// draining each remaining cgroup in turn passes a first-round check and still costs the sum.
	// Read ORDER is what distinguishes them -- both orders read each cgroup the same number of
	// times, so an assertion on counts would pass either.
	order := mgr.readOrder

	// Every cgroup needs the same number of reads here, so none drops out early and the order
	// must be exactly `reads` whole rounds. That exactness is what lets each round be checked.
	require.Len(t, order, len(WorkloadProcessTypes)*readsBeforeFrozen,
		"expected %d whole rounds over %d cgroups, got %v",
		readsBeforeFrozen, len(WorkloadProcessTypes), order)

	for i := 0; i < len(order); i += len(WorkloadProcessTypes) {
		round := order[i : i+len(WorkloadProcessTypes)]
		for _, pt := range WorkloadProcessTypes {
			assert.Contains(t, round, pt,
				"cgroups must be polled together in every round: %s missing from round %d (%v); full order %v",
				pt, i/len(WorkloadProcessTypes)+1, round, order)
		}
	}
}

// TestFreeze_UnreadableStateIsCountedFailedNotAwaited covers the read-error arm of the
// wait. A cgroup removed mid-sweep reports ENOENT and a threaded one rejects the read, so
// this is expected rather than exceptional: the error is surfaced, but the result survives
// alongside it, the cgroup is counted failed rather than notFrozen -- the two say different
// things to the pause path -- and it is dropped from the poll set instead of waiting out a
// budget on a state that can never be read. The remaining cgroup is still awaited normally.
func TestFreeze_UnreadableStateIsCountedFailedNotAwaited(t *testing.T) {
	t.Parallel()

	mgr := newFakeFreezeManager()
	mgr.frozenErr[WorkloadProcessTypes[0]] = errors.New("read cgroup.events: no such file or directory")
	f := NewWorkloadFreezer(mgr)

	start := time.Now()
	res, err := f.Freeze(t.Context(), 10*time.Second)
	elapsed := time.Since(start)

	require.Error(t, err, "the failure is surfaced, as it is for a rejected write")
	assert.Equal(t, len(WorkloadProcessTypes), res.Requested, "every write still landed")
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, len(WorkloadProcessTypes)-1, res.Frozen)
	assert.Zero(t, res.NotFrozen, "an unreadable state is not a workload refusing to stop")
	assert.False(t, res.AllFrozen())
	assert.Less(t, elapsed, time.Second,
		"a cgroup whose state cannot be read must be dropped from the poll, not waited out")
}

func TestFreeze_CountsWriteFailuresWithoutAborting(t *testing.T) {
	t.Parallel()
	mgr := newFakeFreezeManager()
	// A threaded cgroup rejects cgroup.freeze; that is expected, not exceptional, and
	// must not stop the sweep reaching the remaining cgroups.
	mgr.freezeErr[WorkloadProcessTypes[0]] = errors.New("invalid argument")
	f := NewWorkloadFreezer(mgr)

	res, err := f.Freeze(t.Context(), time.Second)
	require.Error(t, err, "the failure is surfaced")
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, len(WorkloadProcessTypes)-1, res.Requested)
	assert.Equal(t, len(WorkloadProcessTypes)-1, res.Frozen,
		"the cgroups that did freeze are still read back frozen")
}

func TestFreeze_ZeroBudgetSkipsTheWait(t *testing.T) {
	t.Parallel()
	mgr := newFakeFreezeManager()
	f := NewWorkloadFreezer(mgr)

	res, err := f.Freeze(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, len(WorkloadProcessTypes), res.Requested)
	assert.Zero(t, res.Frozen, "no budget means the state was never read")
	assert.Equal(t, len(WorkloadProcessTypes), res.NotFrozen)
}

// TestFreeze_CancellationStopsThePollAndReleasesTheLock pins that the caller's ctx bounds
// the state read, not just the lock acquire. The poll holds the freeze lock, so a caller
// that has gone away must not leave us spending the rest of its budget in there: the
// rollback /unfreeze and the resume thaw both queue behind that lock.
//
// The window is widest when the budget outlives the caller's real deadline -- the shared
// client caps requests at 10s, so a budget above that is abandoned by the client while
// envd is still legitimately waiting.
func TestFreeze_CancellationStopsThePollAndReleasesTheLock(t *testing.T) {
	t.Parallel()
	mgr := newFakeFreezeManager()
	// Never reads frozen, so only the budget or cancellation can end the wait.
	for _, pt := range WorkloadProcessTypes {
		mgr.frozenAt[pt] = 1 << 30
	}
	f := NewWorkloadFreezer(mgr)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := f.Freeze(ctx, 10*time.Second)
	elapsed := time.Since(start)

	require.NoError(t, err, "cancellation is not a freeze failure: the writes still landed")
	assert.Less(t, elapsed, 2*time.Second,
		"the poll must stop when the caller goes away, not run out the 10s budget")

	// The lock must be free immediately, or the rollback thaw queues behind a caller
	// that is no longer there.
	unfrozen := make(chan struct{})
	go func() {
		_ = f.Unfreeze(t.Context())
		close(unfrozen)
	}()
	select {
	case <-unfrozen:
	case <-time.After(2 * time.Second):
		t.Fatal("Unfreeze blocked: the freeze lock was still held after cancellation")
	}
}
