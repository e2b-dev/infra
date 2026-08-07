//go:build linux

package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These exercise the swap's decision FLOW through the swapIO seam: which phases run,
// in what order, and — the property that broke twice in review — whether the
// destructive rollback is invoked. TestClassifyEnvdState pins the table in isolation;
// a table can't tell you the caller consulted it correctly, or that an indeterminate
// read never reaches `rm`. No debugfs, NBD, systemd or privileges needed.

// statFound renders a `debugfs stat` header for a regular file with the given mode.
func statFound(mode string, size int) string {
	return fmt.Sprintf("Inode: 12   Type: regular    Mode:  %s   Flags: 0x80000\n"+
		"User:     0   Group:     0   Project:     0   Size: %d\n", mode, size)
}

const statNotFound = "/usr/bin/envd: File not found by ext2_lookup\n"

// fakeDebugfs scripts one reply per phase and records what was asked of it. A `dump`
// script writes dumpBody[phase] to the target path parsed out of the script, so the
// production code's own file handling (pre-create, size check, hashing) still runs.
type fakeDebugfs struct {
	mu       sync.Mutex
	phases   []string          // every phase invoked, in order
	stdout   map[string]string // phase -> stdout
	dumpBody map[string]string // phase -> bytes to leave in the dump target
	errs     map[string]error  // phase -> process-level error
}

var dumpTargetRe = regexp.MustCompile(`^dump\s+\S+\s+(\S+)`)

func (f *fakeDebugfs) io(stageDir string) swapIO {
	return swapIO{stageDir: stageDir, run: f.run}
}

func (f *fakeDebugfs) run(_ context.Context, phase, script string, _ bool) ([]byte, error) {
	f.mu.Lock()
	f.phases = append(f.phases, phase)
	f.mu.Unlock()

	if m := dumpTargetRe.FindStringSubmatch(strings.TrimSpace(script)); m != nil {
		// Mirror debugfs: the target is left as-is (empty) unless a body is scripted.
		if body, ok := f.dumpBody[phase]; ok {
			if err := os.WriteFile(m[1], []byte(body), 0o644); err != nil {
				return nil, err
			}
		}
	}

	return []byte(f.stdout[phase]), f.errs[phase]
}

func (f *fakeDebugfs) ran(phase string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Contains(f.phases, phase)
}

// newSwapFixture stages a source binary and returns the fake plus its swapIO.
// `target` is the content the swap intends to install; `orig` what the rootfs held.
func newSwapFixture(t *testing.T, target string) (*fakeDebugfs, swapIO, string) {
	t.Helper()

	stage := t.TempDir()
	require.NoError(t, os.Chmod(stage, 0o755))
	src := filepath.Join(t.TempDir(), "envd.src")
	require.NoError(t, os.WriteFile(src, []byte(target), 0o755))

	f := &fakeDebugfs{
		stdout:   map[string]string{},
		dumpBody: map[string]string{},
		errs:     map[string]error{},
	}

	return f, f.io(stage), src
}

// TestSwapFlowAbsentRollsBack is the regression that a dev-cluster run caught after
// review and CI had both passed it: `rm` landed, `write` did not, so the inode is
// GONE. That is knowledge, not ignorance, and it must drive the rollback — an earlier
// version read the resulting zero-byte dump as "device unreadable" and failed the boot
// without restoring anything, leaving the sandbox unable to resume at all.
func TestSwapFlowAbsentRollsBack(t *testing.T) {
	t.Parallel()

	f, dbg, src := newSwapFixture(t, "NEW-BINARY")
	f.stdout["size-stat"] = statFound("0755", 4096)
	f.dumpBody["backup"] = "ORIGINAL-BINARY" // the backup succeeds, so rollback is possible
	f.stdout["state-stat"] = statNotFound    // post-swap: envd is gone
	// The rollback restores the original and its read-back confirms it.
	f.stdout["rollback-verify-stat"] = statFound("0755", 15)
	f.dumpBody["rollback-verify"] = "ORIGINAL-BINARY"

	err := swapEnvd(t.Context(), dbg, src)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrOfflineSwapUnrecoverable,
		"a successful rollback is recoverable — the guest boots its original envd")
	assert.Contains(t, err.Error(), "original envd restored")
	assert.True(t, f.ran("rollback"), "an absent binary MUST be rolled back")
	assert.False(t, f.ran("state"),
		"nothing to dump once stat says absent — reading anyway is what made this look like a dead device")
}

// TestSwapFlowUnreadableNeverRollsBack is the converse safety property: when the read
// establishes nothing, the rollback must NOT run, because it begins with `rm` and we
// would be destroying an envd we cannot see on a device we cannot read.
func TestSwapFlowUnreadableNeverRollsBack(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(f *fakeDebugfs)
	}{
		{"stat itself fails", func(f *fakeDebugfs) {
			f.errs["state-stat"] = errors.New("jail failed to start")
		}},
		{"inode present but dump yields nothing", func(f *fakeDebugfs) {
			f.stdout["state-stat"] = statFound("0755", 4096) // present, executable
			// no dumpBody for "state" -> the target stays empty, as a silent exit-0 failure
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, dbg, src := newSwapFixture(t, "NEW-BINARY")
			f.stdout["size-stat"] = statFound("0755", 4096)
			f.dumpBody["backup"] = "ORIGINAL-BINARY"
			tc.setup(f)

			err := swapEnvd(t.Context(), dbg, src)

			require.Error(t, err)
			require.ErrorIs(t, err, ErrOfflineSwapUnrecoverable,
				"an indeterminate rootfs must fail the boot so the overlay is discarded")
			assert.False(t, f.ran("rollback"),
				"rollback starts with rm — it must never run on a rootfs we could not read")
		})
	}
}

// TestSwapFlowAppliedAndIntact covers the two outcomes that must leave the rootfs
// alone. The `applied` case is the one an earlier design broke: it took a second
// opinion from a separate verify pass and treated any disagreement as failure, so one
// glitched read ran the destructive rollback across a rootfs the swap had just fixed.
func TestSwapFlowAppliedAndIntact(t *testing.T) {
	t.Parallel()

	t.Run("target in place -> success", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "NEW-BINARY")
		f.stdout["size-stat"] = statFound("0755", 4096)
		f.dumpBody["backup"] = "ORIGINAL-BINARY"
		f.stdout["state-stat"] = statFound("0755", 10)
		f.dumpBody["state"] = "NEW-BINARY"

		require.NoError(t, swapEnvd(t.Context(), dbg, src))
		assert.False(t, f.ran("rollback"), "a landed swap must not be rolled back")
	})

	t.Run("original untouched -> recoverable, no rollback", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "NEW-BINARY")
		f.stdout["size-stat"] = statFound("0755", 4096)
		f.dumpBody["backup"] = "ORIGINAL-BINARY"
		f.errs["swap"] = errors.New("context deadline exceeded") // the swap never ran
		f.stdout["state-stat"] = statFound("0755", 15)
		f.dumpBody["state"] = "ORIGINAL-BINARY"

		err := swapEnvd(t.Context(), dbg, src)

		require.Error(t, err)
		require.NotErrorIs(t, err, ErrOfflineSwapUnrecoverable)
		assert.Contains(t, err.Error(), "original left in place")
		assert.False(t, f.ran("rollback"), "an intact original must not be rm'ed and rewritten")
	})

	t.Run("target present but not executable -> rollback", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "NEW-BINARY")
		f.stdout["size-stat"] = statFound("0755", 4096)
		f.dumpBody["backup"] = "ORIGINAL-BINARY"
		f.stdout["state-stat"] = statFound("0644", 10) // right bytes, cannot exec
		f.dumpBody["state"] = "NEW-BINARY"
		f.stdout["rollback-verify-stat"] = statFound("0755", 15)
		f.dumpBody["rollback-verify"] = "ORIGINAL-BINARY"

		err := swapEnvd(t.Context(), dbg, src)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "original envd restored")
		assert.True(t, f.ran("rollback"),
			"content matched the target, so only the mode check can catch this")
	})
}

// TestSwapFlowRollbackIgnoresProcessExit pins verify-then-decide on the rollback: a
// non-zero debugfs exit AFTER the restore already landed (a kill, or RuntimeMaxSec
// firing) says nothing about the disk, and must not be reported as unrecoverable.
func TestSwapFlowRollbackIgnoresProcessExit(t *testing.T) {
	t.Parallel()

	f, dbg, src := newSwapFixture(t, "NEW-BINARY")
	f.stdout["size-stat"] = statFound("0755", 4096)
	f.dumpBody["backup"] = "ORIGINAL-BINARY"
	f.stdout["state-stat"] = statNotFound
	f.errs["rollback"] = errors.New("signal: killed") // died after writing
	f.stdout["rollback-verify-stat"] = statFound("0755", 15)
	f.dumpBody["rollback-verify"] = "ORIGINAL-BINARY" // but the restore IS on disk

	err := swapEnvd(t.Context(), dbg, src)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrOfflineSwapUnrecoverable,
		"the read-back decides, not the exit status")
	assert.Contains(t, err.Error(), "original envd restored")
}

// TestSwapFlowRollbackFailureIsUnrecoverable is the one case that legitimately fails
// the boot after a rollback attempt: envd was damaged AND the restore did not land.
func TestSwapFlowRollbackFailureIsUnrecoverable(t *testing.T) {
	t.Parallel()

	f, dbg, src := newSwapFixture(t, "NEW-BINARY")
	f.stdout["size-stat"] = statFound("0755", 4096)
	f.dumpBody["backup"] = "ORIGINAL-BINARY"
	f.stdout["state-stat"] = statNotFound
	f.stdout["rollback-verify-stat"] = statNotFound // the restore did not take

	err := swapEnvd(t.Context(), dbg, src)

	require.ErrorIs(t, err, ErrOfflineSwapUnrecoverable)
	assert.True(t, f.ran("rollback"))
}

// TestSwapFlowRefusalsHappenBeforeMutating pins the guards that must fire before the
// swap touches anything: an oversized rootfs envd (guest-controlled size, host-side
// dumps), a backup that produced nothing (nothing to roll back to), and an empty
// staged source (whose digest an empty read-back would match, faking success).
func TestSwapFlowRefusalsHappenBeforeMutating(t *testing.T) {
	t.Parallel()

	t.Run("oversized envd", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "NEW-BINARY")
		f.stdout["size-stat"] = statFound("0755", maxEnvdSize+1)

		err := swapEnvd(t.Context(), dbg, src)

		require.ErrorIs(t, err, ErrEnvdTooLarge)
		assert.False(t, f.ran("backup"), "must refuse before dumping anything to the host")
		assert.False(t, f.ran("swap"))
	})

	t.Run("backup produced nothing", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "NEW-BINARY")
		f.stdout["size-stat"] = statFound("0755", 4096)
		// no dumpBody for "backup" -> empty backup file

		err := swapEnvd(t.Context(), dbg, src)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing offline swap")
		assert.False(t, f.ran("swap"), "no rollback source means the swap must not start")
	})

	t.Run("empty staged source", func(t *testing.T) {
		t.Parallel()

		f, dbg, src := newSwapFixture(t, "")
		f.stdout["size-stat"] = statFound("0755", 4096)

		err := swapEnvd(t.Context(), dbg, src)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "is empty or unreadable")
		assert.False(t, f.ran("size-stat"), "refused before touching the device at all")
	})
}
