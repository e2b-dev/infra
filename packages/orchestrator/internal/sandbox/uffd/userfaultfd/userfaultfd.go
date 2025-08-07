package userfaultfd

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type userfaultfd struct {
	fd       uintptr
	copyMode CULong
}

// flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK
func NewUserfaultfd(flags uintptr, wp bool) (*userfaultfd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("userfaultfd syscall failed: %v", errno)
	}

	return NewUserfaultfdFromFd(uffd, wp), nil
}

func NewUserfaultfdFromFd(fd uintptr, wp bool) *userfaultfd {
	copyMode := CULong(0)
	if wp {
		copyMode = UFFDIO_COPY_MODE_WP
	}

	return &userfaultfd{
		fd:       fd,
		copyMode: copyMode,
	}
}

// features: UFFD_FEATURE_MISSING_HUGETLBFS
// This is already called by the FC
func (u *userfaultfd) ConfigureApi(features CULong) error {
	api := NewUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING
// This is already called by the FC, but only with the UFFDIO_REGISTER_MODE_MISSING
// We need to call it with UFFDIO_REGISTER_MODE_WP when we use both missing and wp
func (u *userfaultfd) Register(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (u *userfaultfd) writeProtect(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioWriteProtect(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_WRITEPROTECT, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_WRITEPROTECT ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (u *userfaultfd) RemoveWriteProtection(addr uintptr, size uint64) error {
	return u.writeProtect(addr, size, 0)
}

func (u *userfaultfd) AddWriteProtection(addr uintptr, size uint64) error {
	return u.writeProtect(addr, size, UFFDIO_WRITEPROTECT_MODE_WP)
}

// mode: UFFDIO_COPY_MODE_WP
// When we use both missing and wp, we need to use UFFDIO_COPY_MODE_WP, otherwise copying would unprotect the page
func (u *userfaultfd) Copy(addr CULong, data []byte, pagesize int64) error {
	cpy := NewUffdioCopy(data, addr&^CULong(pagesize-1), CULong(pagesize), u.copyMode, 0)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_COPY, uintptr(unsafe.Pointer(&cpy))); errno != 0 {
		return errno
	}

	return nil
}

func (u *userfaultfd) Close() error {
	return syscall.Close(int(u.fd))
}
