package userfaultfd

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// Used for testing (flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK).
func newFd(flags uintptr) (Fd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return 0, fmt.Errorf("userfaultfd syscall failed: %w", errno)
	}

	return Fd(uffd), nil
}

// configureApi is used for testing. The caller (FC in production) sets
// UFFD_FEATURE_MISSING_HUGETLBFS only when hugepages are in use.
func configureApi(f Fd, pagesize uint64, removeEnabled bool) error {
	var features CULong

	// Only set the hugepage feature if we're using hugepages.
	if pagesize == header.HugepageSize {
		features |= UFFD_FEATURE_MISSING_HUGETLBFS
	}

	features |= UFFD_FEATURE_WP_ASYNC
	if removeEnabled {
		features |= UFFD_FEATURE_EVENT_REMOVE
	}

	api := newUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// unregister tears down the UFFD registration over [addr, addr+size).
// Used in test cleanup so in-flight REMOVE events queued by the kernel
// (configureApi enables UFFD_FEATURE_EVENT_REMOVE when removeEnabled)
// don't keep munmap blocked on un-acked events.
func unregister(f Fd, addr uintptr, size uint64) error {
	r := newUffdioRange(CULong(addr), CULong(size))

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_UNREGISTER, uintptr(unsafe.Pointer(&r)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_UNREGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// register is used for testing. FC uses UFFDIO_REGISTER_MODE_MISSING; we add
// UFFDIO_REGISTER_MODE_WP when both missing and write-protection are needed.
func register(f Fd, addr uintptr, size uint64, mode CULong) error {
	register := newUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}
