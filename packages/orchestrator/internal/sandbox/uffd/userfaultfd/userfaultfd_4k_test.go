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

func TestUffdMissing(t *testing.T) {
	pagesize := int64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, false)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init4KPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

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

func TestUffdWriteProtect(t *testing.T) {
	pagesize := int64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init4KPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	fdExit, err := NewFdExit()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	mappings := newMockMappings(memoryStart, size, pagesize)

	go func() {
		err := uffd.Serve(mappings, data, fdExit)
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	memoryArea[0] = 'A'

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}

func TestUffdWriteProtectWithMissing(t *testing.T) {
	pagesize := int64(header.PageSize)

	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init4KPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

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

	d, err := data.Slice(0, pagesize)
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(memoryArea[0:pagesize], d) {
		t.Fatalf("content mismatch: want %q, got %q", d, memoryArea[:pagesize])
	}

	memoryArea[0] = 'A'

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}

// We are trying to simulate registering the missing handler in the FC and then registering the missing+wp handler again in the orchestrator
func TestUffdWriteProtectWithMissingDoubleRegistration(t *testing.T) {
	pagesize := int64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init4KPageMmap(size)

	// done in the FC
	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	// TODO: Can we reregister after triggering missing and still properly handle such a page later?

	// done little later in the orchestrator
	// both flags needs to be present
	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

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

	d, err := data.Slice(0, pagesize)
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(memoryArea[0:pagesize], d) {
		t.Fatalf("content mismatch: want %q, got %q", d, memoryArea[:pagesize])
	}

	memoryArea[0] = 'A'

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}

// TODO: test version with missing pages too (+ `UFFDIO_COPY_MODE_WP`)
func TestUffdWriteProtectWithAsync(t *testing.T) {
	pagesize := int64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	// It seems that passind this feature might be required if we want to enable it as behaves differently without it (still reporting dirty pages when we check proc pages, but more?)
	err = uffd.ConfigureApi(UFFD_FEATURE_WP_ASYNC)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := init4KPageMmap(size)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, uint64(size))
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

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

	for page := range pagemap {
		fmt.Printf("dirty page: %v\n", page)
	}
}
