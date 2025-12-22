package userfaultfd

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

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

// mode: UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING
// This is already called by the FC, but only with the UFFDIO_REGISTER_MODE_MISSING
// We need to call it with UFFDIO_REGISTER_MODE_WP when we use both missing and wp
func register(f Fd, addr uintptr, size uint64, mode CULong) error {
	register := newUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}
