package userfaultfd

// https://docs.kernel.org/admin-guide/mm/userfaultfd.html
// https://man7.org/linux/man-pages/man2/userfaultfd.2.html
// https://github.com/torvalds/linux/blob/master/fs/userfaultfd.c
// https://github.com/loopholelabs/userfaultfd-go/tree/main/pkg/constants

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
import "unsafe"

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
	UFFDIO_WRITEPROTECT = C.UFFDIO_WRITEPROTECT
	UFFDIO_COPY         = C.UFFDIO_COPY

	UFFD_PAGEFAULT_FLAG_WP    = C.UFFD_PAGEFAULT_FLAG_WP
	UFFD_PAGEFAULT_FLAG_WRITE = C.UFFD_PAGEFAULT_FLAG_WRITE

	UFFD_FEATURE_MISSING_HUGETLBFS  = C.UFFD_FEATURE_MISSING_HUGETLBFS
	UFFD_FEATURE_WP_HUGETLBFS_SHMEM = C.UFFD_FEATURE_WP_HUGETLBFS_SHMEM
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

func NewUffdioAPI(api, features CULong) UffdioAPI {
	return UffdioAPI{
		api:      api,
		features: features,
	}
}

func NewUffdioRegister(start, length, mode CULong) UffdioRegister {
	return UffdioRegister{
		_range: UffdioRange{
			start: start,
			len:   length,
		},
		mode: mode,
	}
}

func NewUffdioCopy(b []byte, address CULong, pagesize CULong, mode CULong, copy CLong) UffdioCopy {
	return UffdioCopy{
		src:  CULong(uintptr(unsafe.Pointer(&b[0]))),
		dst:  address &^ CULong(pagesize-1),
		len:  pagesize,
		mode: mode,
		copy: copy,
	}
}

func NewUffdioWriteProtect(start, length, mode CULong) UffdioWriteProtect {
	return UffdioWriteProtect{
		_range: UffdioRange{
			start: start,
			len:   length,
		},
		mode: mode,
	}
}

func GetMsgEvent(msg *UffdMsg) CUChar {
	return msg.event
}

func GetMsgArg(msg *UffdMsg) [24]byte {
	return msg.arg
}

func GetPagefaultAddress(pagefault *UffdPagefault) CULong {
	return pagefault.address
}

func IsWritePageFault(pagefault *UffdPagefault) bool {
	return pagefault.flags&UFFD_PAGEFAULT_FLAG_WRITE != 0
}

func IsWriteProtectPageFault(pagefault *UffdPagefault) bool {
	return pagefault.flags&UFFD_PAGEFAULT_FLAG_WP != 0
}
