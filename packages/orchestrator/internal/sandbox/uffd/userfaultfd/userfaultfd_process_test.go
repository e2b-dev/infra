package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var (
	testCrossProcessPageSize                   = int64(header.HugepageSize)
	testCrossProcessData, testCrossProcessSize = prepareTestData(testCrossProcessPageSize)
)

// Main process, FC in our case
func TestCrossProcessDoubleRegistration(t *testing.T) {
	memoryArea, memoryStart := init2MPageMmap(testCrossProcessSize)

	uffd, err := NewUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, true)
	if err != nil {
		t.Fatal("failed to create userfaultfd", err)
	}
	defer uffd.Close()

	err = uffd.ConfigureApi(0)
	if err != nil {
		t.Fatal("failed to configure uffd api", err)
	}

	err = uffd.Register(memoryStart, uint64(testCrossProcessSize), UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		t.Fatal("failed to register memory", err)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGUSR1)

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", start))

	// Passing the fd to the child process
	uffdFile := os.NewFile(uffd.fd, "userfaultfd")

	cmd.ExtraFiles = []*os.File{uffdFile}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	<-sigc
	fmt.Println("✓ child signaled ready")

	data := bytes.NewReader(content)

	servedContent := make([]byte, pagesize)
	_, err = data.ReadAt(servedContent, 0)
	if err != nil {
		t.Fatal("cannot read content", err)
	}

	if !bytes.Equal(b[0:pagesize], servedContent) {
		t.Fatal("content mismatch", string(servedContent))
	}
}

// Secondary process, orchestrator in our case
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Println("Started >><<")

	size := testCrossProcessSize
	pagesize := testCrossProcessPageSize

	content := repeatToSize(crossContent, size)

	data := bytes.NewReader(content)

	mmapStart := os.Getenv("GO_MMAP_START")
	startRaw, err := strconv.Atoi(mmapStart)
	if err != nil {
		fmt.Print("exit parsing mmap start", err)
		os.Exit(1)
	}

	start := uintptr(startRaw)

	uffdFile := os.NewFile(uintptr(3), "userfaultfd")
	uffdFd := uffdFile.Fd()

	uffd := userfaultfd{
		fd:       uffdFd,
		pagesize: pagesize,
		dirty:    make(map[uintptr]struct{}),
	}

	// done in the FC
	// Check: The reregistration works
	err = uffd.Register(start, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	if err != nil {
		fmt.Print("exit registering uffd", err)
		os.Exit(1)
	}

	fmt.Println("after register")

	ppid := os.Getppid()
	syscall.Kill(ppid, syscall.SIGUSR1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		err := uffd.Serve(ctx, start, data, UFFDIO_COPY_MODE_WP)
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}
	}()

	time.Sleep(10 * time.Second)

	os.Exit(0)
}
