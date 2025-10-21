package testutils

import (
	"fmt"
	"math"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func NewPageMmap(size, pagesize uint64) ([]byte, uintptr, func() error, error) {
	if pagesize == header.PageSize {
		return newMmap(size, header.PageSize, 0)
	}

	if pagesize == header.HugepageSize {
		return newMmap(size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
	}

	return nil, 0, nil, fmt.Errorf("unsupported page size: %d", pagesize)
}

// Even though UFFD behaves differently with file backend memory (and hugetlbfs file backed), the FC uses MAP_PRIVATE|MAP_ANONYMOUS, so the following stub is correct to test for FC.
// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html#write-protect-notifications
// - https://github.com/firecracker-microvm/firecracker/blob/a305f362d0e6f7ba926c73e65452cb51262a44d8/src/vmm/src/persist.rs#L499
func newMmap(size, pagesize uint64, flags int) ([]byte, uintptr, func() error, error) {
	l := int(math.Ceil(float64(size)/float64(pagesize)) * float64(pagesize))
	b, err := syscall.Mmap(
		-1,
		0,
		l,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS|flags,
	)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("failed to mmap: %w", err)
	}

	closeMmap := func() error {
		return syscall.Munmap(b)
	}

	return b, uintptr(unsafe.Pointer(&b[0])), closeMmap, nil
}
