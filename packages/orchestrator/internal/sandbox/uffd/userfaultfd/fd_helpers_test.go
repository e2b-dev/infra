package userfaultfd

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// mockFd is a mock implementation of the Fd interface.
// It allows us to test the handling methods separately from the actual uffd serve loop.
type mockFd struct {
	// The channels send back the info about the uffd handled operations
	// and also allows us to block the methods to test the flow.
	copyCh         chan *blockedEvent[UffdioCopy]
	writeProtectCh chan *blockedEvent[UffdioWriteProtect]
}

func newMockFd() *mockFd {
	return &mockFd{
		copyCh:         make(chan *blockedEvent[UffdioCopy]),
		writeProtectCh: make(chan *blockedEvent[UffdioWriteProtect]),
	}
}

func (m *mockFd) register(_ uintptr, _ uint64, _ CULong) error {
	return nil
}

func (m *mockFd) unregister(_, _ uintptr) error {
	return nil
}

func (m *mockFd) copy(addr, pagesize uintptr, _ []byte, mode CULong) error {
	// Don't use the uffdioCopy constructor as it unsafely checks slice address and fails for arbitrary pointer.
	e := newBlockedEvent(UffdioCopy{
		src:  0,
		dst:  CULong(addr),
		len:  CULong(pagesize),
		mode: mode,
		copy: 0,
	})

	m.copyCh <- e

	<-e.resolved

	return nil
}

func (m *mockFd) writeProtect(addr, size uintptr, mode CULong) error {
	e := newBlockedEvent(UffdioWriteProtect{
		_range: newUffdioRange(
			CULong(addr),
			CULong(size),
		),
		mode: mode,
	})

	m.writeProtectCh <- e

	<-e.resolved

	return nil
}

func (m *mockFd) close() error {
	return nil
}

func (m *mockFd) fd() int32 {
	return 0
}

// Used for testing.
// flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK
func newFd(flags uintptr) (Fd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return 0, fmt.Errorf("userfaultfd syscall failed: %w", errno)
	}

	return Fd(uffd), nil
}

// Used for testing
// features: UFFD_FEATURE_MISSING_HUGETLBFS
// This is already called by the FC
func configureApi(f Fd, pagesize uint64) error {
	var features CULong

	// Only set the hugepage feature if we're using hugepages
	if pagesize == header.HugepageSize {
		features |= UFFD_FEATURE_MISSING_HUGETLBFS
	}

	api := newUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// This wrapped event allows us to simulate the finish of processing of events by FC on FC API Pause.
type blockedEvent[T UffdioCopy | UffdioWriteProtect] struct {
	event    T
	resolved chan struct{}
}

func newBlockedEvent[T UffdioCopy | UffdioWriteProtect](event T) *blockedEvent[T] {
	return &blockedEvent[T]{
		event:    event,
		resolved: make(chan struct{}),
	}
}

func (e *blockedEvent[T]) resolve() {
	close(e.resolved)
}
