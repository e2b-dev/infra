//go:build linux

package block

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/edsrzf/mmap-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const faultChildEnv = "BLOCK_FAULT_TEST_CHILD"

// TestRunFaultSafe_MmapFault verifies that a SIGBUS raised by reading a
// memory-mapped file whose backing pages are gone (here: truncated away; in
// production: an unrecoverable disk read error / bad sector) is converted
// into ErrMemoryFault instead of killing the process.
//
// The faulting read runs in a subprocess: without the conversion the fault is
// a fatal runtime error ("unexpected fault address") that no recover() can
// catch, so an in-process test would take the whole test binary down with it.
func TestRunFaultSafe_MmapFault(t *testing.T) {
	if os.Getenv(faultChildEnv) == "1" {
		runFaultSafeMmapFaultChild(t)

		return
	}
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunFaultSafe_MmapFault$", "-test.v")
	cmd.Env = append(os.Environ(), faultChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err,
		"child did not survive the mmap fault: RunFaultSafe must convert SIGBUS to ErrMemoryFault, not crash the process\n%s", out)
}

func runFaultSafeMmapFaultChild(t *testing.T) {
	t.Helper()

	const size = 2 * 4096

	path := filepath.Join(t.TempDir(), "backing")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, f.Truncate(size))

	m, err := mmap.MapRegion(f, size, mmap.RDWR, 0, 0)
	require.NoError(t, err)
	defer m.Unmap()

	// Truncating the backing file leaves every mapped page beyond EOF: the
	// next access faults with SIGBUS (BUS_ADRERR), the same signal an
	// unrecoverable disk read error produces when the kernel pages in a
	// mapped range.
	require.NoError(t, os.Truncate(path, 0))

	buf := make([]byte, size)
	err = RunFaultSafe(func() error {
		copy(buf, m) // the same memmove-from-mmap as Cache.ReadAt

		return nil
	})
	require.ErrorIs(t, err, ErrMemoryFault)
}

func TestRunFaultSafe_PassesThroughResult(t *testing.T) {
	t.Parallel()

	require.NoError(t, RunFaultSafe(func() error { return nil }))

	sentinel := errors.New("boom")
	err := RunFaultSafe(func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	require.NotErrorIs(t, err, ErrMemoryFault)
}

func TestRunFaultSafe_NonFaultPanicPropagates(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "boom", func() {
		_ = RunFaultSafe(func() error { panic("boom") })
	})
}

// faultTestSink defeats dead-store elimination of the nil dereference below.
var faultTestSink int //nolint:gochecknoglobals

func TestRunFaultSafe_NilDerefPanicPropagates(t *testing.T) {
	t.Parallel()

	// A nil-pointer dereference is a program bug, not a backing-store
	// failure — it must keep panicking rather than be masked as
	// ErrMemoryFault.
	require.Panics(t, func() {
		_ = RunFaultSafe(func() error {
			var p *int
			faultTestSink = *p

			return nil
		})
	})
}

//nolint:paralleltest // manipulates the process-wide panic-on-fault setting
func TestRunFaultSafe_RestoresPanicOnFaultFlag(t *testing.T) {
	defer debug.SetPanicOnFault(false)

	for _, prev := range []bool{false, true} {
		debug.SetPanicOnFault(prev)
		_ = RunFaultSafe(func() error { return nil })
		got := debug.SetPanicOnFault(false)
		assert.Equalf(t, prev, got, "flag not restored after clean return (prev=%v)", prev)
	}

	// The flag must be restored even when fn panics.
	debug.SetPanicOnFault(false)
	func() {
		defer func() { _ = recover() }()
		_ = RunFaultSafe(func() error { panic("boom") })
	}()
	assert.False(t, debug.SetPanicOnFault(false), "flag not restored after panic")
}
