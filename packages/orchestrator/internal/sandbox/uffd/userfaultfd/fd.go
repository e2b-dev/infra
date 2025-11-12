package userfaultfd

// https://docs.kernel.org/admin-guide/mm/userfaultfd.html
// https://man7.org/linux/man-pages/man2/userfaultfd.2.html
// https://github.com/torvalds/linux/blob/master/fs/userfaultfd.c
// https://github.com/loopholelabs/userfaultfd-go/blob/main/pkg/constants/cgo.go

/*
#include <sys/syscall.h>
#include <fcntl.h>
#include <linux/userfaultfd.h>
#include <sys/ioctl.h>

struct uffd_pagefault {
	__u64	flags;
	__u64	address;
	__u32 ptid;
};
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	NR_userfaultfd = C.__NR_userfaultfd

	UFFD_API             = C.UFFD_API
	UFFD_EVENT_PAGEFAULT = C.UFFD_EVENT_PAGEFAULT

	UFFDIO_REGISTER_MODE_MISSING = C.UFFDIO_REGISTER_MODE_MISSING
	UFFDIO_REGISTER_MODE_WP      = C.UFFDIO_REGISTER_MODE_WP

	UFFDIO_WRITEPROTECT_MODE_WP = C.UFFDIO_WRITEPROTECT_MODE_WP
	UFFDIO_COPY_MODE_WP         = C.UFFDIO_COPY_MODE_WP

	UFFDIO_API          = C.UFFDIO_API
	UFFDIO_REGISTER     = C.UFFDIO_REGISTER
	UFFDIO_UNREGISTER   = C.UFFDIO_UNREGISTER
	UFFDIO_COPY         = C.UFFDIO_COPY
	UFFDIO_WRITEPROTECT = C.UFFDIO_WRITEPROTECT

	UFFD_PAGEFAULT_FLAG_WRITE = C.UFFD_PAGEFAULT_FLAG_WRITE
	UFFD_PAGEFAULT_FLAG_WP    = C.UFFD_PAGEFAULT_FLAG_WP

	UFFD_FEATURE_MISSING_HUGETLBFS = C.UFFD_FEATURE_MISSING_HUGETLBFS
)

type (
	CULong = C.ulonglong
	CUChar = C.uchar
	CLong  = C.longlong

	UffdMsg       = C.struct_uffd_msg
	UffdPagefault = C.struct_uffd_pagefault

	UffdioAPI          = C.struct_uffdio_api
	UffdioRegister     = C.struct_uffdio_register
	UffdioRange        = C.struct_uffdio_range
	UffdioCopy         = C.struct_uffdio_copy
	UffdioWriteProtect = C.struct_uffdio_writeprotect
)

func newUffdioAPI(api, features CULong) UffdioAPI {
	return UffdioAPI{
		api:      api,
		features: features,
	}
}

func newUffdioRange(start, length CULong) UffdioRange {
	return UffdioRange{
		start: start,
		len:   length,
	}
}

func newUffdioRegister(start, length, mode CULong) UffdioRegister {
	return UffdioRegister{
		_range: newUffdioRange(start, length),
		mode:   mode,
	}
}

func newUffdioWriteProtect(start, length, mode CULong) UffdioWriteProtect {
	return UffdioWriteProtect{
		_range: newUffdioRange(start, length),
		mode:   mode,
	}
}

func newUffdioCopy(b []byte, address CULong, pagesize CULong, mode CULong, bytesCopied CLong) UffdioCopy {
	return UffdioCopy{
		src:  CULong(uintptr(unsafe.Pointer(&b[0]))),
		dst:  address,
		len:  pagesize,
		mode: mode,
		copy: bytesCopied,
	}
}

func getMsgEvent(msg *UffdMsg) CUChar {
	return msg.event
}

func getMsgArg(msg *UffdMsg) [24]byte {
	return msg.arg
}

func getPagefaultAddress(pagefault *UffdPagefault) uintptr {
	return uintptr(pagefault.address)
}

// Fd is a helper type that wraps uffd fd.
type Fd uintptr

// flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK
func newFd(flags uintptr) (Fd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return 0, fmt.Errorf("userfaultfd syscall failed: %w", errno)
	}

	return Fd(uffd), nil
}

// features: UFFD_FEATURE_MISSING_HUGETLBFS
// This is already called by the FC
func (f Fd) configureApi(pagesize uint64) error {
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
func (f Fd) register(addr uintptr, size uint64, mode CULong) error {
	register := newUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

func (f Fd) unregister(addr, size uintptr) error {
	r := newUffdioRange(CULong(addr), CULong(size))

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_UNREGISTER, uintptr(unsafe.Pointer(&r)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_UNREGISTER ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_COPY_MODE_WP
// When we use both missing and wp, we need to use UFFDIO_COPY_MODE_WP, otherwise copying would unprotect the page
func (f Fd) copy(addr, pagesize uintptr, data []byte, mode CULong) error {
	cpy := newUffdioCopy(data, CULong(addr)&^CULong(pagesize-1), CULong(pagesize), mode, 0)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_COPY, uintptr(unsafe.Pointer(&cpy))); errno != 0 {
		return errno
	}

	// Check if the copied size matches the requested pagesize
	if cpy.copy != CLong(pagesize) {
		return fmt.Errorf("UFFDIO_COPY copied %d bytes, expected %d", cpy.copy, pagesize)
	}

	return nil
}

func (f Fd) writeProtect(addr, size uintptr, mode CULong) error {
	register := newUffdioWriteProtect(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f), UFFDIO_WRITEPROTECT, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_WRITEPROTECT ioctl failed: %w (ret=%d)", errno, ret)
	}

	return nil
}

func (f Fd) removeWriteProtection(addr, size uintptr) error {
	return f.writeProtect(addr, size, 0)
}

func (f Fd) addWriteProtection(addr, size uintptr) error {
	return f.writeProtect(addr, size, UFFDIO_WRITEPROTECT_MODE_WP)
}

func (f Fd) close() error {
	return syscall.Close(int(f))
}

func (f Fd) fd() int32 {
	return int32(f)
}
