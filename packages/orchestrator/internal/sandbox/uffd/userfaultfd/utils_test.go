package userfaultfd

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	pagesInTestData = 32
)

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

func init4KPageMmap(size int64) ([]byte, uintptr) {
	return initMmap(size, header.PageSize, 0)
}

func init2MPageMmap(size int64) ([]byte, uintptr) {
	return initMmap(size, header.HugepageSize, unix.MAP_HUGETLB|unix.MAP_HUGE_2MB)
}

func initMmap(size, pagesize int64, flags int) ([]byte, uintptr) {
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

// getDirtyPages returns a map of dirty pages that were marked by the async uffd write protect
// The /proc/pagemap tracks on the 4K granularity—if a hugepage tracked this way is dirty, all 512 4K pages are marked as dirty
func getDirtyPages(pid int, start, end uintptr) (map[uintptr]struct{}, error) {
	path := fmt.Sprintf("/proc/%d/pagemap", pid)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pageSize := os.Getpagesize()
	results := make(map[uintptr]struct{})

	buf := make([]byte, 8)
	for addr := start; addr < end; addr += uintptr(pageSize) {
		index := addr / uintptr(pageSize)
		offset := int64(index * 8)

		// read exactly one 8-byte entry
		if _, err := f.ReadAt(buf, offset); err != nil {
			return nil, err
		}
		entry := binary.LittleEndian.Uint64(buf)

		// extract bit 57
		wp := (entry>>57)&1 == 1
		if !wp {
			results[addr] = struct{}{}
		}
	}

	return results, nil
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
