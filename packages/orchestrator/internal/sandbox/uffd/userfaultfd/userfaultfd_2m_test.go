package userfaultfd

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestUffdMissingHugepages(t *testing.T) {
	pagesize := int64(header.HugepageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, false)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	// It seems the feature flags are not necessary when configuring the API—can pass 0 or UFFD_FEATURE_MISSING_HUGETLBFS
	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init2MPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	mappings := newTestMappings(memoryStart, size, pagesize)

	fdExit, err := NewFdExit()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit)
		if err != nil {
			fmt.Println("[TestUffdMissing] failed to serve uffd", err)
		}
	}()

	d, err := data.Slice(0, pagesize)
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(memoryArea[0:pagesize], d) {
		t.Fatalf("content mismatch: want %q, got %q", d, memoryArea[:pagesize])
	}
}

func TestUffdWriteProtectHugepages(t *testing.T) {
	pagesize := int64(header.HugepageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	// It seems the feature flags are not necessary when configuring the API—can pass 0 or UFFD_FEATURE_WP_HUGETLBFS_SHMEM
	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init2MPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newTestMappings(memoryStart, size, pagesize)

	fdExit, err := NewFdExit()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit)
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	writtenPages := make(map[uintptr]struct{})

	memoryArea[0] = 'A'
	writtenPages[uintptr(unsafe.Pointer(&memoryArea[0]))] = struct{}{}

	// Test if this dirties the correct page/what is the granularity
	// b[PageSize4K*2] = 'A'

	memoryArea[pagesize*4] = 'A'
	writtenPages[uintptr(unsafe.Pointer(&memoryArea[pagesize*3]))] = struct{}{}

	// TODO: Start capturing the dirty pages

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}

// TODO: test version with missing pages too (+ `UFFDIO_COPY_MODE_WP`)
func TestUffdWriteProtectHugepagesWithAsync(t *testing.T) {
	pagesize := int64(header.HugepageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	// This might also be optional (as in the dirty bits get written nevertheless)
	err = uffd.ConfigureApi(UFFD_FEATURE_WP_ASYNC | UFFD_FEATURE_WP_HUGETLBFS_SHMEM)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init2MPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newTestMappings(memoryStart, size, pagesize)

	fdExit, err := NewFdExit()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit)
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	writtenDirtyPages := make(map[uintptr]struct{})

	memoryArea[0] = 'A'
	writtenDirtyPages[uintptr(unsafe.Pointer(&memoryArea[0]))] = struct{}{}

	memoryArea[pagesize*3] = 'A'
	writtenDirtyPages[uintptr(unsafe.Pointer(&memoryArea[pagesize*3]))] = struct{}{}

	pagemap, err := getDirtyPages(os.Getpid(), memoryStart, memoryStart+uintptr(size))
	if err != nil {
		t.Fatal("failed to check wp bits", err)
	}

	for addr := range writtenDirtyPages {
		if _, ok := pagemap[addr]; !ok {
			t.Fatalf("dirty page not found: %v", addr)
		}
	}

	// The /proc/pagemap still tracks on the 4K granularity, but if hugepage is dirty, all 512 4K pages are marked as dirty
	fmt.Println("number of dirty pages", len(pagemap))
}
