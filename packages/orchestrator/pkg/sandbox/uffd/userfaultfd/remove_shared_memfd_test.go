//go:build linux

package userfaultfd

import (
	"bytes"
	"encoding/binary"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// These tests probe the kernel semantics the REMOVE → Zero → Empty(uuid.Nil)
// pause bookkeeping relies on: after a REMOVE event, the handler assumes the
// range reads as zeros for the rest of the session, so DiffMetadata maps it
// to uuid.Nil and the resumed sandbox gets zero pages there.
//
// That assumption holds for MAP_PRIVATE|MAP_ANONYMOUS guest memory (the only
// layout the existing suite tests — see testutils/page_mmap.go) where
// MADV_DONTNEED discards content and any re-access raises a MISSING fault
// the handler answers with zeros.
//
// It does NOT hold for MAP_SHARED memfd-backed guest memory (the use-memfd
// layout, where FC hands the orchestrator the memfd in the UFFD handshake):
// MADV_DONTNEED only zaps page tables, the content survives in the page
// cache, and a later access silently remaps the OLD content with no UFFD
// event at all — shmem/hugetlb MISSING faults fire only when the page is
// absent from the page cache, and MINOR mode is not registered (the serve
// loop treats a MINOR event as fatal). The tracker keeps the block in Zero,
// the next pause maps it uuid.Nil, and the resumed guest reads zeros where
// the pre-pause guest could still read its data: live pages get nilled.
func TestMadvDontneed_AnonPrivate_ContentDiscarded(t *testing.T) {
	t.Parallel()

	b, err := syscall.Mmap(-1, 0, int(header.PageSize),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Munmap(b) })

	for i := range b {
		b[i] = 0xAB
	}
	require.NoError(t, unix.Madvise(b, unix.MADV_DONTNEED))

	require.Equal(t, byte(0x00), b[0],
		"anon-private DONTNEED discards content: REMOVE→Zero bookkeeping is sound here")
}

func TestMadvDontneed_SharedMemfd_ContentSurvives(t *testing.T) {
	t.Parallel()

	fd, err := unix.MemfdCreate("dontneed-semantics", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(fd) })
	require.NoError(t, unix.Ftruncate(fd, int64(header.PageSize)))

	b, err := syscall.Mmap(fd, 0, int(header.PageSize),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Munmap(b) })

	for i := range b {
		b[i] = 0xAB
	}
	require.NoError(t, unix.Madvise(b, unix.MADV_DONTNEED))

	require.Equal(t, byte(0xAB), b[0],
		"shared-memfd DONTNEED retains content: a guest re-read returns the old bytes, "+
			"so marking the range Zero→Empty→uuid.Nil at pause zeroes live data on resume")
}

// uffdUserModeOnly is UFFD_USER_MODE_ONLY: allows unprivileged userfaultfd
// (vm.unprivileged_userfaultfd=0) for user-mode faults, which is all this
// test triggers. Production never creates the fd itself (FC sends it over
// the handshake socket), so this flag is test-only.
const uffdUserModeOnly = 1

// TestSharedMemfd_ReaccessAfterRemove_NoUffdEvent is the full demonstration
// against a real userfaultfd on a MAP_SHARED memfd:
//
//  1. populate a page with a sentinel, register MISSING + EVENT_REMOVE,
//  2. MADV_DONTNEED it → the kernel delivers UFFD_EVENT_REMOVE (this is the
//     event the production tracker turns into block.Zero),
//  3. re-access the page → it must NOT raise any UFFD event, and it reads
//     the ORIGINAL sentinel, not zeros.
//
// Step 3 is exactly the state the pause path mishandles: tracker says Zero
// (→ Empty → uuid.Nil in the diff header) while the guest-visible content is
// non-zero. Skipped when userfaultfd creation is not permitted.
func TestSharedMemfd_ReaccessAfterRemove_NoUffdEvent(t *testing.T) {
	t.Parallel()

	const numPages = 4
	size := int(header.PageSize) * numPages

	memfd, err := unix.MemfdCreate("uffd-remove-semantics", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(memfd) })
	require.NoError(t, unix.Ftruncate(memfd, int64(size)))

	mem, err := syscall.Mmap(memfd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Munmap(mem) })

	// Populate page 0 before registering so no MISSING fault is pending.
	sentinel := bytes.Repeat([]byte{0xC3}, int(header.PageSize))
	copy(mem[:header.PageSize], sentinel)

	uffd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		uffd, err = newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK | uffdUserModeOnly)
	}
	if err != nil {
		t.Skipf("userfaultfd unavailable (vm.unprivileged_userfaultfd=0 and no CAP): %v", err)
	}
	t.Cleanup(func() { _ = uffd.close() })

	require.NoError(t, configureApi(uffd, header.PageSize, true)) // EVENT_REMOVE on
	start := uintptr(unsafe.Pointer(&mem[0]))
	require.NoError(t, register(uffd, start, uint64(size), UFFDIO_REGISTER_MODE_MISSING))
	t.Cleanup(func() { _ = unregister(uffd, start, uint64(size)) })

	// MADV_DONTNEED blocks until the REMOVE event is read, so issue it on a
	// goroutine and drain the event here (mirroring the serve loop).
	madviseDone := make(chan error, 1)
	go func() {
		madviseDone <- unix.Madvise(mem[:header.PageSize], unix.MADV_DONTNEED)
	}()

	rmStart, rmEnd := readRemoveEvent(t, uffd)
	require.NoError(t, <-madviseDone)
	require.Equal(t, uint64(start), rmStart, "REMOVE start covers page 0")
	require.Equal(t, uint64(start)+uint64(header.PageSize), rmEnd, "REMOVE end covers page 0")
	// At this point the production tracker would have set the block to Zero.

	// Re-access page 0. On the shared memfd this must complete WITHOUT a
	// MISSING fault (page still in the page cache); nobody is serving this
	// uffd, so if a fault were raised the populate would hang — the watchdog
	// converts that into a diagnostic instead of a stuck test.
	populateDone := make(chan error, 1)
	go func() {
		populateDone <- unix.Madvise(mem[:header.PageSize], unix.MADV_POPULATE_READ)
	}()
	select {
	case err := <-populateDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		_ = uffd.close()
		t.Fatal("re-access raised a MISSING fault: anon-like semantics (REMOVE→Zero would be sound)")
	}

	// No event was generated by the re-access...
	n, _, pollErr := pollUffd(uffd, 200*time.Millisecond)
	require.NoError(t, pollErr)
	require.Zero(t, n, "re-access after REMOVE must not deliver any UFFD event")

	// ...and the page reads the ORIGINAL content, not zeros: the tracker's
	// Zero state is now a lie, and the next pause would map this page to
	// uuid.Nil — zeroing live guest data on resume.
	require.Equal(t, sentinel, mem[:header.PageSize],
		"content silently restored after REMOVE: pause would nil a non-zero page")
}

// TestSharedMemfd_WriteThenStaleRemove_DirtyEvidenceDestroyed demonstrates
// the stale free-page-hint kill chain with the production discard primitive
// (e2b FC fork 431f1fc: memfd ranges are discarded with madvise(MADV_REMOVE),
// which punches the hole AND delivers UFFD_EVENT_REMOVE). The fork discards
// on every free-page *hint* as it arrives (balloon device.rs,
// process_free_page_hinting_queue), but hints are advisory: the guest does
// not hold hinted pages out of its allocator, so it can re-allocate and
// WRITE a hinted block before the discard lands — and the pre-pause drain
// runs with vCPUs live. Using the same pagemap dirty rule as FC's WP-async
// scan (present + WP clear → dirty; see async_wp_test.go):
//
//  1. page installed clean (present + uffd-WP set),
//  2. guest WRITES it — WP_ASYNC resolves silently, pagemap now reads
//     dirty (present + WP clear). This write should reach the snapshot.
//  3. the stale hint triggers MADV_REMOVE on the page:
//     - the written DATA IS DESTROYED (hole punched in the memfd),
//     - UFFD_EVENT_REMOVE is delivered → the production tracker sets the
//     block to Zero,
//     - the PTE is zapped → the pagemap entry is no longer present, so the
//     "was written" evidence is unrecoverable by any pagemap scan: the
//     block drops out of FC's dirty bitmap, and empty.AndNot(dirty)
//     cannot rescue it from the Empty set.
//
// At the next pause the block is in inputEmpty → mapped to uuid.Nil → the
// resumed guest reads ZEROS where it wrote real data. (The live guest is
// already corrupted the moment the discard lands; the snapshot preserves
// the damage.) Skipped when userfaultfd creation is not permitted.
func TestSharedMemfd_WriteThenStaleRemove_DirtyEvidenceDestroyed(t *testing.T) {
	t.Parallel()

	const numPages = 4
	size := int(header.PageSize) * numPages

	memfd, err := unix.MemfdCreate("uffd-stale-hint", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(memfd) })
	require.NoError(t, unix.Ftruncate(memfd, int64(size)))

	mem, err := syscall.Mmap(memfd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Munmap(mem) })

	// Page 0 present before registration (a served fault in production).
	copy(mem[:header.PageSize], bytes.Repeat([]byte{0x11}, int(header.PageSize)))

	uffd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		uffd, err = newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK | uffdUserModeOnly)
	}
	if err != nil {
		t.Skipf("userfaultfd unavailable (vm.unprivileged_userfaultfd=0 and no CAP): %v", err)
	}
	t.Cleanup(func() { _ = uffd.close() })

	require.NoError(t, configureApi(uffd, header.PageSize, true)) // WP_ASYNC + EVENT_REMOVE
	start := uintptr(unsafe.Pointer(&mem[0]))
	require.NoError(t, register(uffd, start, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))
	t.Cleanup(func() { _ = unregister(uffd, start, uint64(size)) })

	pm, err := newSelfPagemap()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pm.Close() })

	// (1) Clean state: write-protect the present page.
	require.NoError(t, uffd.writeProtect(start, uintptr(header.PageSize), UFFDIO_WRITEPROTECT_MODE_WP))
	entry, err := pm.read(start)
	require.NoError(t, err)
	require.True(t, entry&pmPresentBit != 0 && entry&pmUffdWPBit != 0,
		"precondition: page present + WP set (clean)")

	// (2) Guest write: WP_ASYNC resolves it with no event; pagemap = dirty.
	mem[0] = 0xEE
	entry, err = pm.read(start)
	require.NoError(t, err)
	require.True(t, entry&pmPresentBit != 0 && entry&pmUffdWPBit == 0,
		"after write: present + WP clear — FC's scan would report this page DIRTY")

	// (3) Stale free-page hint → MADV_REMOVE (the 431f1fc discard primitive)
	// → REMOVE event (tracker → Zero) + hole punched in the memfd.
	madviseDone := make(chan error, 1)
	go func() {
		madviseDone <- unix.Madvise(mem[:header.PageSize], unix.MADV_REMOVE)
	}()
	rmStart, _ := readRemoveEvent(t, uffd)
	require.NoError(t, <-madviseDone)
	require.Equal(t, uint64(start), rmStart)

	// The guest's written data is destroyed: the hole reads zeros straight
	// from the memfd (no fault needed to observe it).
	fileByte := make([]byte, 1)
	_, err = unix.Pread(memfd, fileByte, 0)
	require.NoError(t, err)
	require.Equal(t, byte(0x00), fileByte[0],
		"MADV_REMOVE destroyed the guest's written byte (hole punched)")

	// And the dirty evidence is gone with it: page no longer present, so a
	// pagemap scan (present && !WP) reports it CLEAN — the block drops out
	// of FC's dirty bitmap and empty.AndNot(dirty) cannot rescue it from
	// the Empty set. Pause → uuid.Nil → resume reads zeros where the guest
	// wrote 0xEE moments before the drain.
	entry, err = pm.read(start)
	require.NoError(t, err)
	require.Zero(t, entry&pmPresentBit,
		"after MADV_REMOVE: page not present — the write is invisible to FC's WP-async dirty scan")
}

const (
	pmUffdWPBit  = uint64(1) << 57
	pmPresentBit = uint64(1) << 63
)

// selfPagemap reads /proc/self/pagemap entries (testutils.PagemapReader is in
// a different package whose import would be fine, but the two bits we need
// make a local reader simpler).
type selfPagemap struct{ f *os.File }

func newSelfPagemap() (*selfPagemap, error) {
	f, err := os.Open("/proc/self/pagemap")
	if err != nil {
		return nil, err
	}

	return &selfPagemap{f: f}, nil
}

func (p *selfPagemap) Close() error { return p.f.Close() }

func (p *selfPagemap) read(addr uintptr) (uint64, error) {
	var buf [8]byte
	if _, err := p.f.ReadAt(buf[:], int64(uint64(addr)/4096*8)); err != nil {
		return 0, err
	}

	return binary.NativeEndian.Uint64(buf[:]), nil
}

// TestHugetlbMemfd_MisalignedRemove_MarksLivePagesZero reproduces the
// hugepage variant of the accidental-nil bug against the production discard
// primitive (e2b FC fork commit 431f1fc: balloon discards memfd ranges with
// madvise(MADV_REMOVE)).
//
// The balloon works at guest 4 KiB granularity, so discard ranges are 4 KiB
// aligned, not hugepage aligned. For such a range on a hugetlbfs MAP_SHARED
// memfd:
//
//   - hugetlbfs punches INWARD: only whole hugepages fully inside the range
//     are discarded (start rounds up, end rounds down),
//   - the UFFD_EVENT_REMOVE reports the ORIGINAL unaligned range,
//   - the serve loop (userfaultfd.go REMOVE batch) assumes the event is
//     u.pageSize-aligned ("UFFD invariant" comment), rounds the start DOWN
//     via BlockIdx and derives the end from len/pageSize — so a misaligned
//     event marks a hugepage Zero whose content was NEVER discarded.
//
// A Zero block whose content survived is then mapped to uuid.Nil at the next
// pause: the resumed guest reads zeros over live memory.
//
// Layout: 3 hugepages [0,6M), all filled with a sentinel. MADV_REMOVE over
// [1M,5M): the kernel punches only [2M,4M), the event covers [1M,5M), and
// the production arithmetic marks [0,4M) Zero — block 0 is marked Zero while
// it still holds 2 MiB of live data.
//
// Skipped when hugepages or userfaultfd are unavailable.
func TestHugetlbMemfd_MisalignedRemove_MarksLivePagesZero(t *testing.T) {
	t.Parallel()

	hugepage := int(header.HugepageSize)
	size := 3 * hugepage

	memfd, err := unix.MemfdCreate("uffd-hugetlb-remove", unix.MFD_HUGETLB|unix.MFD_HUGE_2MB)
	if err != nil {
		t.Skipf("hugetlb memfd unavailable: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(memfd) })
	require.NoError(t, unix.Ftruncate(memfd, int64(size)))

	mem, err := syscall.Mmap(memfd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		t.Skipf("hugetlb mmap unavailable (check /proc/sys/vm/nr_hugepages): %v", err)
	}
	t.Cleanup(func() { _ = syscall.Munmap(mem) })

	// Populate all three hugepages before registering (live guest data).
	for i := range mem {
		mem[i] = 0xC3
	}

	uffd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		uffd, err = newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK | uffdUserModeOnly)
	}
	if err != nil {
		t.Skipf("userfaultfd unavailable: %v", err)
	}
	t.Cleanup(func() { _ = uffd.close() })

	require.NoError(t, configureApi(uffd, header.HugepageSize, true)) // MISSING_HUGETLBFS + EVENT_REMOVE
	base := uintptr(unsafe.Pointer(&mem[0]))
	require.NoError(t, register(uffd, base, uint64(size), UFFDIO_REGISTER_MODE_MISSING))
	t.Cleanup(func() { _ = unregister(uffd, base, uint64(size)) })

	// Balloon-shaped discard: 4 KiB-aligned, NOT hugepage-aligned.
	const mib = 1 << 20
	rangeStart, rangeLen := 1*mib, 4*mib
	madviseDone := make(chan error, 1)
	go func() {
		madviseDone <- unix.Madvise(mem[rangeStart:rangeStart+rangeLen], unix.MADV_REMOVE)
	}()

	rmStart, rmEnd := readRemoveEvent(t, uffd)
	require.NoError(t, <-madviseDone)

	// The event reports the original unaligned range — the serve loop's
	// "page-aligned to u.pageSize (UFFD invariant)" comment is false here.
	require.Equal(t, uint64(base)+uint64(rangeStart), rmStart, "event start is the unaligned madvise start")
	require.Equal(t, uint64(base)+uint64(rangeStart+rangeLen), rmEnd, "event end is the unaligned madvise end")
	require.NotZero(t, rmStart%uint64(header.HugepageSize),
		"event start is NOT hugepage-aligned, violating the serve loop's assumed invariant")

	// Production arithmetic from userfaultfd.go's REMOVE batch:
	//   startIdx := BlockIdx(startOff, pageSize); endIdx := startIdx + len/pageSize
	startOff := int64(rmStart - uint64(base))
	startIdx := header.BlockIdx(startOff, int64(header.HugepageSize))
	endIdx := startIdx + int64(rmEnd-rmStart)/int64(header.HugepageSize)
	require.EqualValues(t, 0, startIdx, "handler marks from block 0 (start rounded DOWN into live data)")
	require.EqualValues(t, 2, endIdx, "handler marks blocks [0,2) = bytes [0,4M) as Zero")

	// Ground truth (observed): hugetlbfs removes the full hugepage [2M,4M)
	// and ZEROES the partial head [1M,2M) and tail [4M,5M) in place
	// (hugetlb_zero_partial_page). So the guest-visible content is:
	//   [0,1M)  live sentinel    [1M,5M) zeros    [5M,6M) live sentinel
	require.Equal(t, byte(0xC3), mem[0], "head [0,1M) untouched — live data")
	require.Equal(t, byte(0x00), mem[2*mib-1], "partial head [1M,2M) zeroed in place")
	require.Equal(t, byte(0x00), mem[4*mib], "partial tail [4M,5M) zeroed in place")
	require.Equal(t, byte(0xC3), mem[5*mib], "outside the range — live data")

	// The handler's marked range [0,4M) disagrees with reality in BOTH
	// directions:
	//
	//  1. Block 0 is marked Zero but [0,1M) is live: the next pause maps
	//     [0,2M) to uuid.Nil and the resume serves zeros over 1 MiB of live
	//     guest memory — accidental nil.
	//  2. Block 2 ([4M,6M)) is NOT marked, but the kernel zeroed [4M,5M):
	//     if the guest never rewrites it, the pause keeps the parent mapping
	//     and the resume serves the STALE pre-discard content where the
	//     guest observed zeros.
	require.Less(t, startIdx, int64(1), "marked range swallows live block 0: nilled at pause")
	require.Less(t, endIdx, int64(3), "zeroed tail in block 2 is unmarked: stale content at resume")
}

// readRemoveEvent polls the uffd until one UFFD_EVENT_REMOVE arrives and
// returns its [start, end) range.
func readRemoveEvent(t *testing.T, uffd Fd) (start, end uint64) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, unsafe.Sizeof(UffdMsg{}))
	for {
		require.True(t, time.Now().Before(deadline), "timed out waiting for UFFD_EVENT_REMOVE")

		n, err := syscall.Read(int(uffd), buf)
		if err == syscall.EAGAIN {
			time.Sleep(5 * time.Millisecond)

			continue
		}
		require.NoError(t, err)
		require.Equal(t, int(unsafe.Sizeof(UffdMsg{})), n)

		msg := (*UffdMsg)(unsafe.Pointer(&buf[0]))
		require.EqualValues(t, UFFD_EVENT_REMOVE, getMsgEvent(msg), "expected a REMOVE event")

		arg := getMsgArg(msg)
		rm := (*UffdRemove)(unsafe.Pointer(&arg[0]))

		return uint64(rm.start), uint64(rm.end)
	}
}

// pollUffd polls the uffd for readability, returning the number of ready fds.
func pollUffd(uffd Fd, timeout time.Duration) (int, int16, error) {
	fds := []unix.PollFd{{Fd: int32(uffd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(timeout.Milliseconds()))

	return n, fds[0].Revents, err
}
