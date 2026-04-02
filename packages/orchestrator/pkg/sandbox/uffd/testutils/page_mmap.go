package testutils

import (
	"errors"
	"fmt"
	"math"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func NewPageMmap(t *testing.T, size, pagesize uint64) ([]byte, uintptr, error) {
	t.Helper()

	if pagesize == header.PageSize {
		return newMmap(t, size, header.PageSize, 0)
	}

	if pagesize == header.HugepageSize {
		b, addr, err := newMmap(t, size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
		// Hugepage allocation can fail with ENOMEM on CI runners that don't
		// have enough (or any) hugepages pre-allocated in /proc/sys/vm/nr_hugepages.
		// Skip gracefully rather than failing the test.
		if err != nil && errors.Is(err, syscall.ENOMEM) {
			pages := int(math.Ceil(float64(size) / float64(header.HugepageSize)))
			t.Skipf("skipping: hugepage mmap failed (need %d hugepages): %v", pages, err)
		}

		return b, addr, err
	}

	return nil, 0, fmt.Errorf("unsupported page size: %d", pagesize)
}

// Even though UFFD behaves differently with file backend memory (and hugetlbfs file backed), the FC uses MAP_PRIVATE|MAP_ANONYMOUS, so the following stub is correct to test for FC.
// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html#write-protect-notifications
// - https://github.com/firecracker-microvm/firecracker/blob/a305f362d0e6f7ba926c73e65452cb51262a44d8/src/vmm/src/persist.rs#L499
func newMmap(t *testing.T, size, pagesize uint64, flags int) ([]byte, uintptr, error) {
	t.Helper()

	l := int(math.Ceil(float64(size)/float64(pagesize)) * float64(pagesize))
	b, err := syscall.Mmap(
		-1,
		0,
		l,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS|flags,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to mmap: %w", err)
	}

	t.Cleanup(func() {
		syscall.Munmap(b)
	})

	return b, uintptr(unsafe.Pointer(&b[0])), nil
}
