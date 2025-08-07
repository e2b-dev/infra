package userfaultfd

import (
	"math"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const pagesInTestData = 32

type mockMappings struct {
	start    uintptr
	size     int64
	pagesize int64
}

func newMockMappings(start uintptr, size, pagesize int64) *mockMappings {
	return &mockMappings{
		start:    start,
		size:     size,
		pagesize: pagesize,
	}
}

func (m *mockMappings) GetRange(addr uintptr) (int64, int64, error) {
	offset := addr - m.start
	pagesize := m.pagesize

	return int64(offset), pagesize, nil
}

type mockSlicer struct {
	content []byte
}

func newMockSlicer(content []byte) *mockSlicer {
	return &mockSlicer{content: content}
}

func (s *mockSlicer) Slice(offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func newMock4KPageMmap(size int64) ([]byte, uintptr) {
	return newMockMmap(size, header.PageSize, 0)
}

func newMock2MPageMmap(size int64) ([]byte, uintptr) {
	return newMockMmap(size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
}

func newMockMmap(size, pagesize int64, flags int) ([]byte, uintptr) {
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

func repeatToSize(src []byte, size int64) []byte {
	if len(src) == 0 || size <= 0 {
		return nil
	}

	dst := make([]byte, size)
	for i := 0; i < int(size); i += len(src) {
		end := i + len(src)
		if end > int(size) {
			end = int(size)
		}
		copy(dst[i:end], src[:end-i])
	}

	return dst
}

func prepareTestData(pagesize int64) (data *mockSlicer, size int64) {
	size = pagesize * pagesInTestData

	data = newMockSlicer(
		repeatToSize(
			[]byte("Hello from userfaultfd! This is our test content that should be readable after the page fault."),
			size,
		),
	)

	return data, size
}
