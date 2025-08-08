package userfaultfd

import (
	"bytes"
	"fmt"
	"syscall"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"go.uber.org/zap"
)

func TestUffdMissing(t *testing.T) {
	pagesize := uint64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.configureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := newMock4KPageMmap(size)

	err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

	fdExit, err := fdexit.New()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit, zap.L())
		if err != nil {
			fmt.Println("[TestUffdMissing] failed to serve uffd", err)
		}
	}()

	d, err := data.Slice(0, int64(pagesize))
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(memoryArea[0:pagesize], d) {
		t.Fatalf("content mismatch: want %q, got %q", d, memoryArea[:pagesize])
	}
}

func TestUffdWriteProtect(t *testing.T) {
	pagesize := uint64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.configureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := newMock4KPageMmap(size)

	err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, size)
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	fdExit, err := fdexit.New()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	mappings := newMockMappings(memoryStart, size, pagesize)

	go func() {
		err := uffd.Serve(mappings, data, fdExit, zap.L())
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	memoryArea[0] = 'A'

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}

func TestUffdWriteProtectWithMissing(t *testing.T) {
	pagesize := uint64(header.PageSize)

	data, size := prepareTestData(pagesize)

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.configureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := newMock4KPageMmap(size)

	err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, size)
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

	fdExit, err := fdexit.New()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit, zap.L())
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	d, err := data.Slice(0, int64(pagesize))
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
	pagesize := uint64(header.PageSize)
	data, size := prepareTestData(pagesize)

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.configureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	memoryArea, memoryStart := newMock4KPageMmap(size)

	// done in the FC
	err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	// TODO: Can we reregister after triggering missing and still properly handle such a page later?

	// done little later in the orchestrator
	// both flags needs to be present
	err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	err = uffd.AddWriteProtection(memoryStart, size)
	if err != nil {
		t.Fatal("failed to write protect memory", err)
	}

	mappings := newMockMappings(memoryStart, size, pagesize)

	fdExit, err := fdexit.New()
	if err != nil {
		t.Fatal("failed to create fd exit", err)
	}
	defer fdExit.Close()

	go func() {
		err := uffd.Serve(mappings, data, fdExit, zap.L())
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	d, err := data.Slice(0, int64(pagesize))
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(memoryArea[0:pagesize], d) {
		t.Fatalf("content mismatch: want %q, got %q", d, memoryArea[:pagesize])
	}

	memoryArea[0] = 'A'

	// TODO: the write should be unblocked here, ideally we should also wait to check it was blocked then unblocked from the uffd
}
