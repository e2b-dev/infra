package testutils

import (
	"context"
	"fmt"
	"math"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type mockSlicer struct {
	content []byte
}

func newMockSlicer(content []byte) *mockSlicer {
	return &mockSlicer{content: content}
}

func (s *mockSlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func NewPageMmap(size, pagesize uint64) ([]byte, uintptr, error) {
	if pagesize == header.PageSize {
		return newMockMmap(size, header.PageSize, 0)
	}

	if pagesize == header.HugepageSize {
		return newMockMmap(size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
	}

	panic(fmt.Sprintf("unsupported page size: %d", pagesize))
}

func newMockMmap(size, pagesize uint64, flags int) ([]byte, uintptr, error) {
	l := int(math.Ceil(float64(size)/float64(pagesize)) * float64(pagesize))
	b, err := syscall.Mmap(
		-1,
		0,
		l,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS|flags,
	)
	if err != nil {
		return nil, 0, err
	}

	return b, uintptr(unsafe.Pointer(&b[0])), nil
}
