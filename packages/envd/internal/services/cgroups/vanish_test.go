//go:build linux

package cgroups

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// vanishErrnos are the two a removed cgroup produces, and TestVanish_ErrnosComeFromTheKernel
// pins that claim against real cgroupfs. Every fixture below drives both, because which one
// a site meets depends only on where the removal lands relative to the open -- so a
// single-errno fixture passes against half the bug.
var vanishErrnos = []struct {
	name string
	err  error
}{
	{"ENOENT: removed before the open", syscall.ENOENT},
	{"ENODEV: removed after the open", syscall.ENODEV},
}

// TestVanished_MatchesARemovedCgroupAndNothingElse pins both halves of the predicate. The
// negative half is the load-bearing one: the freeze sweep's Failed count exists to catch a
// cgroup that is STILL THERE and still would not take the write or report its state, so a
// predicate wide enough to swallow a vanished cgroup would swallow the signal it is measured
// against. The issue this fixes proposed "skip on any read error" as an alternative; these
// cases are why that was not taken.
//
// The errnos below are named for what they are, not for a mechanism that produces them: the
// inherited claim that a threaded cgroup rejects cgroup.freeze does not reproduce on cgroup2
// (6.17), where such a write succeeds and reads back frozen. What the count needs is that
// SOME non-vanish error reaches it, which these do.
func TestVanished_MatchesARemovedCgroupAndNothingElse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"bare ENOENT", syscall.ENOENT, true},
		{"bare ENODEV", syscall.ENODEV, true},
		{"fs.ErrNotExist itself", fs.ErrNotExist, true},
		{"ENOENT inside a PathError, as os returns it", &fs.PathError{Op: "open", Err: syscall.ENOENT}, true},
		{"ENODEV inside a PathError, as os returns it", &fs.PathError{Op: "read", Err: syscall.ENODEV}, true},
		{"wrapped again by a caller", errors.New("read x: " + syscall.ENODEV.Error()), false},

		{"nil is not a vanish", nil, false},
		{"EACCES: the cgroup is there and unreadable", syscall.EACCES, false},
		{"EOPNOTSUPP: the kernel refusing the write", syscall.EOPNOTSUPP, false},
		{"EBUSY: the kernel refusing the write", syscall.EBUSY, false},
		{"EIO", syscall.EIO, false},
		{"an unobservable manager is a different condition", ErrFrozenUnobservable, false},
		{"an error carrying no errno at all", errors.New("boom"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, vanished(tc.err))
		})
	}
}

// TestVanish_ErrnosComeFromTheKernel is the premise every other test here rests on: that a
// removed cgroup produces exactly these two errnos and that the ordering picks between them.
// Asserted against real cgroupfs rather than the synthetic tree, because it is a cgroupfs
// property and not a POSIX one -- an ordinary unlinked-but-open file keeps serving reads,
// which is why the tmpfs fixtures below have to inject the errnos instead of causing them.
//
// Needs no root: a delegated cgroup2 slice is writable by its own user.
func TestVanish_ErrnosComeFromTheKernel(t *testing.T) {
	t.Parallel()

	base := delegatedCgroup2Dir(t)

	t.Run("removed before the open reports ENOENT", func(t *testing.T) {
		t.Parallel()

		// t.TempDir() would put this on the system temp filesystem. The errnos under
		// test are produced by cgroupfs, so the directory has to be a real cgroup.
		dir, err := os.MkdirTemp(base, "vanish-") //nolint:usetesting // must live inside cgroupfs
		require.NoError(t, err)
		// The removes below are the assertions, so they stay inline -- but any require
		// between here and them aborts the subtest with a real cgroup left behind, and as
		// root the base IS the cgroup2 mount root. Cleanup is idempotent against that.
		t.Cleanup(func() { _ = os.Remove(dir) })
		require.NoError(t, os.Remove(dir))

		_, readErr := os.ReadFile(filepath.Join(dir, "cgroup.events"))
		require.Error(t, readErr)
		require.ErrorIs(t, readErr, fs.ErrNotExist)
		require.NotErrorIs(t, readErr, syscall.ENODEV, "the two orderings are distinguishable")
		assert.True(t, vanished(readErr))
	})

	t.Run("removed after the open reports ENODEV", func(t *testing.T) {
		t.Parallel()

		// t.TempDir() would put this on the system temp filesystem. The errnos under
		// test are produced by cgroupfs, so the directory has to be a real cgroup.
		dir, err := os.MkdirTemp(base, "vanish-") //nolint:usetesting // must live inside cgroupfs
		require.NoError(t, err)
		// The removes below are the assertions, so they stay inline -- but any require
		// between here and them aborts the subtest with a real cgroup left behind, and as
		// root the base IS the cgroup2 mount root. Cleanup is idempotent against that.
		t.Cleanup(func() { _ = os.Remove(dir) })
		fh, err := os.Open(filepath.Join(dir, "cgroup.events"))
		require.NoError(t, err)
		defer fh.Close()
		require.NoError(t, os.Remove(dir))

		_, readErr := fh.Read(make([]byte, 64))
		require.Error(t, readErr)
		require.ErrorIs(t, readErr, syscall.ENODEV)
		require.NotErrorIs(t, readErr, fs.ErrNotExist,
			"an ENOENT-only guard genuinely does not cover this ordering")
		assert.True(t, vanished(readErr))
	})

	// The freeze and unfreeze writes go through the same open-then-use shape, so they reach
	// the same fork. Recorded because the site inventory this fix worked from listed the
	// write path as ENOENT-only.
	t.Run("the freeze write reports ENODEV once opened", func(t *testing.T) {
		t.Parallel()

		// t.TempDir() would put this on the system temp filesystem. The errnos under
		// test are produced by cgroupfs, so the directory has to be a real cgroup.
		dir, err := os.MkdirTemp(base, "vanish-") //nolint:usetesting // must live inside cgroupfs
		require.NoError(t, err)
		// The removes below are the assertions, so they stay inline -- but any require
		// between here and them aborts the subtest with a real cgroup left behind, and as
		// root the base IS the cgroup2 mount root. Cleanup is idempotent against that.
		t.Cleanup(func() { _ = os.Remove(dir) })
		fh, err := os.OpenFile(filepath.Join(dir, "cgroup.freeze"), os.O_WRONLY|os.O_TRUNC, 0)
		require.NoError(t, err)
		defer fh.Close()
		require.NoError(t, os.Remove(dir))

		_, writeErr := fh.WriteString("1")
		require.Error(t, writeErr)
		require.ErrorIs(t, writeErr, syscall.ENODEV)
		assert.True(t, vanished(writeErr))
	})
}

// delegatedCgroup2Dir finds a cgroup2 directory this user may create cgroups in. systemd
// delegates the per-user manager slice, so an unprivileged run normally has one; root has
// the mount root itself.
func delegatedCgroup2Dir(t *testing.T) string {
	t.Helper()

	const mount = "/sys/fs/cgroup"

	var st unix.Statfs_t
	if err := unix.Statfs(mount, &st); err != nil || st.Type != unix.CGROUP2_SUPER_MAGIC {
		t.Skip("no cgroup2 mount at " + mount)
	}

	uid := os.Getuid()
	// The delegated user manager slice is a SIBLING of the login session scope this test
	// most likely runs in, not an ancestor of it, so walking up from our own cgroup never
	// reaches it. Try it by name first, then our own chain, then the root for a root run.
	candidates := []string{
		filepath.Join(mount, "user.slice", fmt.Sprintf("user-%d.slice", uid), fmt.Sprintf("user@%d.service", uid)),
	}
	if self, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		if rel, ok := strings.CutPrefix(strings.TrimSpace(string(self)), "0::"); ok {
			for dir := filepath.Join(mount, rel); dir != mount; dir = filepath.Dir(dir) {
				candidates = append(candidates, dir)
			}
		}
	}
	candidates = append(candidates, mount)

	for _, dir := range candidates {
		probe, err := os.MkdirTemp(dir, "probe-") //nolint:usetesting // probing this exact directory is the point
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.Remove(probe) })
		require.NoError(t, os.Remove(probe))

		return dir
	}
	t.Skip("no writable cgroup2 directory: needs a delegated user slice or root")

	return ""
}

// newVanishFixture builds a tree whose freezes settle immediately and wraps it so one call
// against "doomed" fails with err. Settling matters: without it nothing ever reads back
// frozen, so every test here would spend its whole budget and report NotFrozen for reasons
// that have nothing to do with the cgroup that vanished.
func newVanishFixture(t *testing.T, method string, err error) (*WorkloadFreezer, string, *failAt) {
	t.Helper()

	f, root := newTreeFixture(t, "/system.slice/envd.service",
		"system.slice", "system.slice/envd.service", "customer", "doomed")
	newSettling(t, f, root)
	inj := &failAt{
		PathManager: f.mgr.(PathManager),
		method:      method,
		target:      filepath.Join(root, "doomed"),
		err:         err,
	}
	f.mgr = inj

	return f, root, inj
}

// TestFreeze_SettlePollCountsAVanishedCgroupApart is the site that fired the false rollout
// escalation: a transient unit that exits between the freeze write and the read-back. It has
// to land as Vanished and not as NotFrozen, because NotFrozen is the outcome that says a
// snapshot may have caught a live workload -- which is the one thing a cgroup that stopped
// existing cannot be doing.
func TestFreeze_SettlePollCountsAVanishedCgroupApart(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			f, _, inj := newVanishFixture(t, failFrozenAt, errno.err)

			res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
			require.NoError(t, err)

			assert.Equal(t, 1, res.Vanished, "the cgroup that went away is counted as such")
			assert.Zero(t, res.Failed, "and not as a failure")
			assert.Zero(t, res.NotFrozen,
				"and above all not as a workload that refused to stop, which is what an uncounted drop becomes")
			assert.Positive(t, res.Frozen, "the rest of the sweep still confirmed")
			assert.Equal(t, int64(1), inj.calls.Load(),
				"read once and dropped from the poll set: a path that cannot come back must not be re-read until the budget expires")
		})
	}
}

// TestFreeze_SweepWriteVanishIsNotRequested covers the other freeze-side site. A write that
// never landed must not put the cgroup into the confirmation set, or tolerating one race
// manufactures a second one at the settle poll.
func TestFreeze_SweepWriteVanishIsNotRequested(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			f, root, _ := newVanishFixture(t, failFreezeAt, errno.err)

			res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
			require.NoError(t, err)

			assert.Equal(t, 1, res.Vanished)
			assert.Zero(t, res.Failed)
			assert.Zero(t, res.NotFrozen, "nothing was requested, so nothing can be outstanding")
			assert.True(t, frozenOnDisk(t, root, "customer"),
				"and the sibling the sweep was walking towards is still frozen")
		})
	}
}

// TestFreeze_AWriteTheKernelRefusedIsStillAFailure is the negative twin of the two above, and
// the reason the predicate names two errnos instead of tolerating every error the way the
// audit does. A cgroup that is still there and refuses the write keeps running, which is a
// real property of the tree about to be snapshotted, and Failed is the count that carries it.
func TestFreeze_AWriteTheKernelRefusedIsStillAFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"EOPNOTSUPP", syscall.EOPNOTSUPP},
		{"EBUSY", syscall.EBUSY},
		{"EACCES", syscall.EACCES},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newVanishFixture(t, failFreezeAt, tc.err)

			res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})

			require.Error(t, err, "a refusal that outlives the cgroup propagates, where a vanish does not")
			require.ErrorIs(t, err, tc.err)
			assert.Equal(t, 1, res.Failed, "and is counted")
			assert.Zero(t, res.Vanished, "and is not excused as a race")
		})
	}
}

// TestFreeze_ScanVanishIsNotAScanFailure is the positive twin of TestFreeze_ScanErrorIsNotFatal,
// which injects EACCES at the same site. ScanFailed drives a warning that some cgroups could
// not be classified as the guest's own; naming one that no longer exists gives a reader
// nothing to act on, and this walk runs in BOTH freeze modes, so it fired on every cluster.
func TestFreeze_ScanVanishIsNotAScanFailure(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newVanishFixture(t, failFreezeRequestedAt, errno.err)

			res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
			require.NoError(t, err)

			assert.Zero(t, res.ScanFailed, "a cgroup that no longer exists froze nothing to preserve")
			assert.Positive(t, res.Requested, "and the sweep still did its work")
		})
	}
}

// TestFreeze_ScanVanishAtChildrenOfIsNotAScanFailure covers the scan's OTHER syscall. The
// removal can land on either, and only the state read was covered above.
//
// ENOENT only, deliberately: readdir is the one converted call that cannot report ENODEV.
// Measured on cgroupfs -- holding the directory fd open across the removal and then reading
// it still gives ENOENT, where the same ordering on a file gives ENODEV. The site is routed
// through the shared predicate anyway rather than kept as a special case.
func TestFreeze_ScanVanishAtChildrenOfIsNotAScanFailure(t *testing.T) {
	t.Parallel()

	f, _, _ := newVanishFixture(t, failChildrenOf, syscall.ENOENT)

	res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
	require.NoError(t, err)

	assert.Zero(t, res.ScanFailed, "a cgroup listed by its parent and gone by the readdir is a race")
	assert.Positive(t, res.Requested, "and the sweep still did its work")
}

// TestFreeze_OutcomesReconcileAgainstRequested pins the arithmetic the Vanished counter exists
// to keep. NotFrozen is derived by difference, so any outcome dropped from the poll set
// without a home silently becomes NotFrozen -- the failure mode this whole change is about.
func TestFreeze_OutcomesReconcileAgainstRequested(t *testing.T) {
	t.Parallel()

	t.Run("a vanished poll closes the read-back line", func(t *testing.T) {
		t.Parallel()

		f, _, _ := newVanishFixture(t, failFrozenAt, syscall.ENODEV)

		res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
		require.NoError(t, err)

		assert.Equal(t, res.Requested, res.Frozen+res.NotFrozen+res.Unobservable+res.Vanished+res.Failed,
			"every cgroup written to reaches exactly one outcome")
	})

	// The write side does NOT reconcile the same way, and asserting it here keeps the identity
	// above from being read as a general one. A cgroup removed before its write was never
	// Requested, so counting it Vanished makes the outcomes sum to MORE than Requested. That is
	// the first accounting line on FreezeResult, not the second, and Vanished spans both
	// exactly as Failed does.
	t.Run("a vanished write is counted outside requested", func(t *testing.T) {
		t.Parallel()

		f, _, _ := newVanishFixture(t, failFreezeAt, syscall.ENOENT)

		res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
		require.NoError(t, err)

		assert.Equal(t, 1, res.Vanished)
		assert.Greater(t, res.Frozen+res.NotFrozen+res.Unobservable+res.Vanished+res.Failed, res.Requested,
			"a write that never landed is counted, but not as something requested")
		assert.Equal(t, res.Requested, res.Frozen+res.NotFrozen+res.Unobservable+res.Failed,
			"and the read-back line still closes on its own")
	})

	// A budget of zero skips the poll outright, so nothing can have vanished during it. The
	// counts must describe the phase that ran rather than the one that did not: everything
	// requested is still outstanding, and Vanished stays empty.
	t.Run("a skipped poll reports no vanishes", func(t *testing.T) {
		t.Parallel()

		f, _, inj := newVanishFixture(t, failFrozenAt, syscall.ENODEV)

		res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 0})
		require.NoError(t, err)

		assert.Zero(t, inj.calls.Load(), "the poll never ran")
		assert.Zero(t, res.Vanished, "so it cannot have observed anything vanishing")
		assert.Equal(t, res.Requested, res.NotFrozen, "and everything it wrote to is still unconfirmed")
	})
}

// TestUnfreeze_ToleratesBothErrnosAtTheThawWrite completes the thaw side. The existing
// discovery test drives the read; this drives the write, which is the call that reported
// ENODEV in production because writeCgroupProp opens before it writes.
//
// Ungateable and live everywhere: the thaw walks the whole tree after a legacy pause too, so
// a dirty result here answers 500 on a thaw that worked, leaves the watchdog armed to fire
// mid-operation, and keeps the next pause from rescanning.
func TestUnfreeze_ToleratesBothErrnosAtTheThawWrite(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			f, root := newTreeFixture(t, "/system.slice/envd.service",
				"system.slice", "system.slice/envd.service", "customer", "doomed")
			freezeSweptSet(t, root, "customer", "doomed")
			f.mgr = &failAt{
				PathManager: f.mgr.(PathManager),
				method:      failUnfreezeAt,
				target:      filepath.Join(root, "doomed"),
				err:         errno.err,
			}

			res, err := f.UnfreezeReporting(t.Context(), DefaultThawMaxCgroups)
			require.NoError(t, err, "a cgroup that vanished mid-thaw is a race, not a failure")
			assert.Zero(t, res.Failed)
			assert.False(t, frozenOnDisk(t, root, "customer"), "and the rest of the tree still thaws")
		})
	}
}

// TestFreeze_StaticCgroupVanishAtThePollIsStillAFailure is the boundary of the whole change,
// and it sits on the DEFAULT configuration: with the hierarchy flag off the settle poll's only
// entries ARE the static cgroups, so a vanish tolerated here would be the single way the new
// counter could ever move -- while reporting a clean freeze for a workload that is still
// running.
//
// The static cgroups are envd's own. One disappearing between the write and the read-back does
// not mean a transient unit exited; rmdir needs the cgroup task-free, so something migrated our
// tasks out and removed it underneath us. The freeze does not follow them, so they are running.
// Before this change that was loud, and it has to stay loud: Failed, a surfaced error, and
// AllFrozen false, which is what stops the live-upgrade handover from swapping.
func TestFreeze_StaticCgroupVanishAtThePollIsStillAFailure(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			mgr := newFakeFreezeManager()
			// A fake manager is not a PathManager, so this is legacy mode -- the default,
			// and the configuration where the poll set is exclusively the static list.
			mgr.frozenErr[WorkloadProcessTypes[0]] = &fs.PathError{
				Op: "read", Path: "/sys/fs/cgroup/user/cgroup.events", Err: errno.err,
			}
			f := NewWorkloadFreezer(mgr)

			res, err := f.Freeze(t.Context(), FreezeOptions{MaxWait: time.Second})

			require.Error(t, err, "a vanished cgroup of OURS is surfaced, not swallowed")
			assert.Equal(t, ModeLegacy, res.Mode)
			assert.Equal(t, 1, res.Failed, "counted as a failure, not excused as guest churn")
			assert.Zero(t, res.Vanished, "the tolerance is for the walk's own targets only")
			assert.False(t, res.AllFrozen(), "which is what holds back the live-upgrade handover")
		})
	}
}

// TestFreeze_VanishedPathsNameTheCgroup covers the only record of WHICH cgroup went away. The
// count alone is not actionable: a vanish raises no error and does not hold back AllFrozen, so
// a sweep that is otherwise clean logs nothing and the path would exist nowhere.
func TestFreeze_VanishedPathsNameTheCgroup(t *testing.T) {
	t.Parallel()

	f, root, _ := newVanishFixture(t, failFrozenAt, syscall.ENODEV)

	res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
	require.NoError(t, err)

	assert.Equal(t, 1, res.Vanished)
	assert.Equal(t, []string{filepath.Join(root, "doomed")}, res.VanishedPaths,
		"the sample names the cgroup the counter is about")
}

// TestSamplePath_IsBounded pins the bound rather than the sample. The log exporter drops any
// line over 192 KiB outright, so an unbounded path list is the one shape that could make the
// line it belongs to disappear entirely -- the opposite of what collecting paths is for.
func TestSamplePath_IsBounded(t *testing.T) {
	t.Parallel()

	var got []string
	for i := range auditPathSample + 4 {
		got = samplePath(got, fmt.Sprintf("/sys/fs/cgroup/churn%d", i))
	}

	assert.Len(t, got, auditPathSample, "capped at the audit's bound")
	assert.Equal(t, "/sys/fs/cgroup/churn0", got[0], "and keeps the FIRST seen, not the last")
}

// probeVanish fails the late-freeze probe for one cgroup and counts writes against it, so a
// test can tell whether the sweep still tried to freeze a path it had just learned was gone.
type probeVanish struct {
	PathManager

	target string
	err    error
	writes atomic.Int64
}

func (m *probeVanish) FreezeRequestedAt(path string) (bool, error) {
	if path == m.target {
		return false, &fs.PathError{Op: "open", Path: path, Err: m.err}
	}

	return m.PathManager.FreezeRequestedAt(path)
}

func (m *probeVanish) FreezeAt(path string) error {
	if path == m.target {
		m.writes.Add(1)
	}

	return m.PathManager.FreezeAt(path) //nolint:wrapcheck // decorator
}

// TestSweepHierarchy_ProbeVanishSkipsTheWrite: once the late-freeze probe has learned a cgroup
// is gone, the write below it would be a second open against that path for the same answer.
// The saving is microseconds, but it is spent inside the freeze budget with the sweep lock
// held, and the vanish classification that makes the short-circuit expressible is new here.
//
// Asserted on the write count rather than on elapsed time: "did the second syscall happen" is
// the structural claim, and a clock would only measure the machine.
func TestSweepHierarchy_ProbeVanishSkipsTheWrite(t *testing.T) {
	t.Parallel()

	for _, errno := range vanishErrnos {
		t.Run(errno.name, func(t *testing.T) {
			t.Parallel()

			f, root := newTreeFixture(t, "/system.slice/envd.service",
				"system.slice", "system.slice/envd.service", "customer", "doomed")
			newSettling(t, f, root)
			inj := &probeVanish{
				PathManager: f.mgr.(PathManager),
				target:      filepath.Join(root, "doomed"),
				err:         errno.err,
			}
			f.mgr = inj

			res, err := f.Freeze(t.Context(), FreezeOptions{Mode: ModeHierarchy, MaxWait: 5 * time.Second})
			require.NoError(t, err)

			assert.Zero(t, inj.writes.Load(), "no write against a path the probe just learned was gone")
			assert.Equal(t, 1, res.Vanished, "and it is still counted")
			assert.Equal(t, []string{filepath.Join(root, "doomed")}, res.VanishedPaths)
			assert.Zero(t, res.Failed)
			assert.True(t, frozenOnDisk(t, root, "customer"), "the rest of the sweep is unaffected")
		})
	}
}
