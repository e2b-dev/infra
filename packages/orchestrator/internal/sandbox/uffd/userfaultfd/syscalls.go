package userfaultfd

import (
	"fmt"
	"syscall"
	"unsafe"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK
func newUserfaultfd(flags uintptr, src block.Slicer, m *memory.Mapping, pagesize int64, logger *zap.Logger) (*Userfaultfd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("userfaultfd syscall failed: %w", errno)
	}

	return NewUserfaultfdFromFd(uffd, src, m, pagesize, logger)
}

// features: UFFD_FEATURE_MISSING_HUGETLBFS
// This is already called by the FC
func (u *Userfaultfd) configureApi(pagesize uint64) error {
	var features CULong

	// Only set the hugepage feature if we're using hugepages
	if pagesize == header.HugepageSize {
		features |= UFFD_FEATURE_MISSING_HUGETLBFS
	}

	api := NewUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING
// This is already called by the FC, but only with the UFFDIO_REGISTER_MODE_MISSING
// We need to call it with UFFDIO_REGISTER_MODE_WP when we use both missing and wp
func (u *Userfaultfd) Register(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

func (u *Userfaultfd) unregister(addr uintptr, size uint64) error {
	r := NewUffdioRange(CULong(addr), CULong(size))

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_UNREGISTER, uintptr(unsafe.Pointer(&r)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_UNREGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_COPY_MODE_WP
// When we use both missing and wp, we need to use UFFDIO_COPY_MODE_WP, otherwise copying would unprotect the page
func (u *Userfaultfd) copy(addr uintptr, data []byte, pagesize uint64, mode CULong) error {
	cpy := NewUffdioCopy(data, CULong(addr)&^CULong(pagesize-1), CULong(pagesize), mode, 0)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_COPY, uintptr(unsafe.Pointer(&cpy))); errno != 0 {
		return errno
	}

	// Check if the copied size matches the requested pagesize
	if uint64(cpy.copy) != pagesize {
		return fmt.Errorf("UFFDIO_COPY copied %d bytes, expected %d", cpy.copy, pagesize)
	}

	return nil
}

func (u *Userfaultfd) Close() error {
	return syscall.Close(int(u.fd))
}
