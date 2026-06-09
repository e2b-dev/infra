//go:build linux

package userfaultfd

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestRemoveEventAfterWPReRegister pins the kernel contract that
// dropMissingEventsTracking relies on: UFFD_EVENT_REMOVE is gated on the VMA
// being associated with a uffd ctx that negotiated UFFD_FEATURE_EVENT_REMOVE —
// not on MISSING registration. It walks three phases on the same region:
//
//	registered (MISSING|WP)        MADV_DONTNEED -> REMOVE event(s)
//	unregistered (no re-arm)       MADV_DONTNEED -> NO event (ctx gone)
//	re-armed WP-only               MADV_DONTNEED -> REMOVE event(s) again
//
// The middle phase shows that without the WP-only re-register the events stop;
// the last shows the WP-only re-register restores them (what the handler does).
//
// It drives the fd directly (no Serve loop). A single MADV_DONTNEED can deliver
// more than one REMOVE message, so each phase fully drains the queue (rather
// than reading a single message) — otherwise a leftover from one phase would be
// misread as an event in the next.
func TestRemoveEventAfterWPReRegister(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("this test requires root privileges (userfaultfd creation)")
	}

	pagesize := uint64(header.HugepageSize)
	size := pagesize * 2

	mem, start, err := testutils.NewPageMmap(t, size, pagesize) // skips if no hugepages
	require.NoError(t, err)

	// Non-blocking fd so the drain loop can read until EAGAIN.
	fd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)
	t.Cleanup(func() { fd.close() })

	// removeEnabled=true negotiates UFFD_FEATURE_EVENT_REMOVE.
	require.NoError(t, configureApi(fd, pagesize, true))
	require.NoError(t, register(fd, start, size, UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))
	t.Cleanup(func() {
		// Unregister before close so a teardown path can't block on an un-acked
		// event (mirrors the cross-process harness cleanup).
		_ = unregister(fd, start, size)
	})

	// Phase 1 — registered (MISSING|WP): a free delivers REMOVE event(s).
	n1 := freeAndDrainRemoves(t, fd, mem, start, uintptr(size))
	t.Logf("phase 1 (registered MISSING|WP): %d REMOVE event(s)", n1)
	require.Positive(t, n1, "registered region should deliver a REMOVE event")

	// Phase 2 — unregister WITHOUT re-arming. The VMA loses its uffd ctx, so a
	// free should emit no REMOVE event at all (userfaultfd_remove returns early).
	require.NoError(t, unregister(fd, start, size))
	n2 := freeAndDrainRemoves(t, fd, mem, start, uintptr(size))
	t.Logf("phase 2 (unregistered, not re-armed): %d REMOVE event(s)", n2)
	require.Zero(t, n2, "unregistered region must deliver no REMOVE event")

	// Phase 3 — re-arm WP-only (what dropMissingEventsTracking does on a hugepage
	// REMOVE): the ctx association is restored, so REMOVE events resume.
	require.NoError(t, register(fd, start, size, UFFDIO_REGISTER_MODE_WP))
	n3 := freeAndDrainRemoves(t, fd, mem, start, uintptr(size))
	t.Logf("phase 3 (WP re-armed): %d REMOVE event(s)", n3)
	require.Positive(t, n3, "WP re-registered region should deliver a REMOVE event again")
}

// freeAndDrainRemoves triggers MADV_DONTNEED over mem (in a goroutine — an
// event-generating free parks until its events are read) and returns the number
// of UFFD_EVENT_REMOVE messages delivered while it ran, asserting every message
// is a REMOVE within [start, start+size). It loops polling + draining until the
// madvise reports completion, so the queue is empty when it returns. A free that
// generates no event (unregistered range) returns immediately → count 0.
func freeAndDrainRemoves(t *testing.T, fd Fd, mem []byte, start, size uintptr) int {
	t.Helper()

	madviseDone := make(chan error, 1)
	go func() { madviseDone <- unix.Madvise(mem, unix.MADV_DONTNEED) }()

	buf := make([]byte, unsafe.Sizeof(UffdMsg{}))
	pfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	deadline := time.Now().Add(5 * time.Second)

	// drain reads every currently-queued message, asserting each is a REMOVE.
	drain := func() (removes int) {
		for {
			rn, err := syscall.Read(int(fd), buf)
			if errors.Is(err, syscall.EAGAIN) {
				return removes
			}
			require.NoError(t, err)
			require.Equal(t, int(unsafe.Sizeof(UffdMsg{})), rn, "short uffd msg read")

			msg := (*UffdMsg)(unsafe.Pointer(&buf[0]))
			require.Equal(t, CUChar(UFFD_EVENT_REMOVE), getMsgEvent(msg), "expected only REMOVE events")

			arg := getMsgArg(msg)
			rm := *(*UffdRemove)(unsafe.Pointer(&arg[0]))
			assert.GreaterOrEqual(t, uint64(rm.start), uint64(start), "REMOVE start in range")
			assert.LessOrEqual(t, uint64(rm.end), uint64(start+size), "REMOVE end in range")
			removes++
		}
	}

	count := 0
	for {
		count += drain()

		select {
		case err := <-madviseDone:
			require.NoError(t, err, "MADV_DONTNEED")
			// userfaultfd_remove runs synchronously inside the syscall, so all
			// events are queued by the time it returns — one final drain.
			count += drain()

			return count
		default:
		}

		if time.Now().After(deadline) {
			t.Fatal("MADV_DONTNEED did not complete within timeout")
		}
		// Wait briefly for the madvise to make progress / queue more events.
		_, err := unix.Poll(pfds, 100)
		require.NoError(t, err)
	}
}
