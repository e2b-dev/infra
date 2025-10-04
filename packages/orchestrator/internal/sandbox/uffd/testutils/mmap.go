package testutils

import (
	"context"
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

func New4KPageMmap(size uint64) ([]byte, uintptr) {
	return newMockMmap(size, header.PageSize, 0)
}

func New2MPageMmap(size uint64) ([]byte, uintptr) {
	return newMockMmap(size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
}

func newMockMmap(size, pagesize uint64, flags int) ([]byte, uintptr) {
	l := int(math.Ceil(float64(size)/float64(pagesize)) * float64(pagesize))
	b, err := syscall.Mmap(
		-1,
		0,
		l,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS|flags,
	)
	if err != nil {
		return nil, 0
	}

	return b, uintptr(unsafe.Pointer(&b[0]))
}
