package userfaultfd

// https://docs.kernel.org/admin-guide/mm/userfaultfd.html
// https://man7.org/linux/man-pages/man2/userfaultfd.2.html
// https://github.com/torvalds/linux/blob/master/fs/userfaultfd.c

/*
#define _GNU_SOURCE
#include <sys/syscall.h>
#include <fcntl.h>
#include <linux/userfaultfd.h>
#include <sys/ioctl.h>

struct uffd_pagefault {
	__u64	flags;
	__u64	address;
	__u32 ptid;
};

static inline unsigned long get_UFFDIO_API(void) {
    return UFFDIO_API;
}

static inline unsigned long get_UFFDIO_REGISTER(void) {
    return UFFDIO_REGISTER;
}

static inline unsigned long get_UFFDIO_COPY(void) {
    return UFFDIO_COPY;
}

static inline unsigned long get_UFFDIO_WRITEPROTECT(void) {
    return UFFDIO_WRITEPROTECT;
}

#ifndef UFFD_FEATURE_WP_ASYNC
  #define UFFD_FEATURE_WP_ASYNC (1ULL << 15)
#endif
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

	UFFDIO_COPY_MODE_WP = C.UFFDIO_COPY_MODE_WP

	UFFD_PAGEFAULT_FLAG_WP    = C.UFFD_PAGEFAULT_FLAG_WP
	UFFD_PAGEFAULT_FLAG_WRITE = C.UFFD_PAGEFAULT_FLAG_WRITE

	UFFD_FEATURE_MISSING_HUGETLBFS  = C.UFFD_FEATURE_MISSING_HUGETLBFS
	UFFD_FEATURE_WP_HUGETLBFS_SHMEM = C.UFFD_FEATURE_WP_HUGETLBFS_SHMEM
	UFFD_FEATURE_WP_ASYNC           = C.UFFD_FEATURE_WP_ASYNC
)

var (
	// UFFDIO_API          = 3222841919 // From <linux/userfaultfd.h> macro
	// UFFDIO_REGISTER     = 3223366144 // From <linux/userfaultfd.h> macro
	// UFFDIO_COPY         = 3223890435 // From <linux/userfaultfd.h> macro
	// UFFDIO_WRITEPROTECT = 3222841862 // From <linux/userfaultfd.h> macro

	// These values are calculated in kernel headers—we can hardcode them and it should be mostly fine,
	// but we can also make them dynamic.
	UFFDIO_API          = uint64(C.get_UFFDIO_API())
	UFFDIO_REGISTER     = uint64(C.get_UFFDIO_REGISTER())
	UFFDIO_COPY         = uint64(C.get_UFFDIO_COPY())
	UFFDIO_WRITEPROTECT = uint64(C.get_UFFDIO_WRITEPROTECT())
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
