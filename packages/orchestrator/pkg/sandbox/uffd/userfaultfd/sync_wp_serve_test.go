//go:build linux

package userfaultfd

// Tests for the synchronous write-protect serve path (resolveWriteProtect):
// a real WP fault is driven end to end through the resolve on a
// write-protected hugetlb page. Reuses hugepageMmap from
// sync_wp_bench_test.go (same package).

import (
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestSyncWPResolveDirectFault drives one real synchronous WP fault through
// resolveWriteProtect: a writer stores to a write-protected hugepage (and
// blocks), the test reads the WP event and resolves it, and we assert the store
// completed, the page is marked dirty, and the counter advanced.
//
//nolint:paralleltest // mutates GOMAXPROCS and disables GC
func TestSyncWPResolveDirectFault(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root (userfaultfd registration)")
	}

	const pagesize = uint64(header.HugepageSize)
	mem, memStart := hugepageMmap(t, pagesize)
	mem[0] = 0x00 // populate (present, unprotected)

	fd, err := newFd(syscall.O_CLOEXEC)
	require.NoError(t, err)
	t.Cleanup(func() { fd.close() })

	api := newUffdioAPI(UFFD_API, 0) // features=0 → synchronous WP
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	require.Zero(t, errno, "UFFDIO_API")
	require.NoError(t, register(fd, memStart, pagesize, UFFDIO_REGISTER_MODE_WP))
	t.Cleanup(func() { _ = unregister(fd, memStart, pagesize) })

	u := &Userfaultfd{
		fd:          fd,
		pageSize:    uintptr(pagesize),
		pageTracker: block.NewTracker(),
	}

	// Arm the whole page for write-protection.
	require.NoError(t, fd.writeProtectRange(memStart, uintptr(pagesize), uintptr(pagesize), UFFDIO_WRITEPROTECT_MODE_WP))

	// No STW can wait on the fault-blocked writer thread.
	if prev := runtime.GOMAXPROCS(0); prev < 2 {
		runtime.GOMAXPROCS(2)
		defer runtime.GOMAXPROCS(prev)
	}
	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runtime.LockOSThread()
		mem[0] = 0x42 // sync WP fault → blocks until resolved below
	}()

	// Read the WP fault event and resolve it. Poll with a deadline instead
	// of a bare blocking read: on a kernel that does not deliver the sync WP
	// fault for this configuration the test must fail with a message, not
	// hang the suite.
	buf := make([]byte, unsafe.Sizeof(UffdMsg{}))
	var addr uintptr
	deadline := time.Now().Add(30 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("no WP fault event within 30s — this kernel may not deliver synchronous WP faults for hugetlb")
		}
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		pn, perr := unix.Poll(pfd, int(remaining.Milliseconds()))
		if perr == unix.EINTR {
			continue
		}
		require.NoError(t, perr)
		if pn == 0 {
			continue
		}
		n, rerr := syscall.Read(int(fd), buf)
		if rerr == syscall.EINTR {
			continue
		}
		require.NoError(t, rerr)
		require.NotZero(t, n)
		msg := (*UffdMsg)(unsafe.Pointer(&buf[0]))
		require.Equal(t, CUChar(UFFD_EVENT_PAGEFAULT), getMsgEvent(msg))
		arg := getMsgArg(msg)
		pf := (*UffdPagefault)(unsafe.Pointer(&arg[0]))
		require.NotZero(t, uint64(pf.flags)&uint64(UFFD_PAGEFAULT_FLAG_WP), "expected a WP fault")
		addr = getPagefaultAddress(pf)

		break
	}

	require.NoError(t, u.resolveWriteProtect(t.Context(), addr, nil, time.Now()))

	wg.Wait() // store unblocked → completed

	assert.Equal(t, byte(0x42), mem[0], "write landed after resolve")
	assert.Equal(t, int64(1), u.wpFaultsResolved.Load(), "one WP fault resolved")
}
