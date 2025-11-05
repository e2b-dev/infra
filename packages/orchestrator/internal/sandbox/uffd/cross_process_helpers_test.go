package uffd

// This tests is creating uffd in a process and handling the page faults in another process.
// It also tests reregistering the uffd with the additional wp flag in the another process (in "orchestrator") after registering the missing handler already (in "FC"),
// simulating the case we have with the write protection being set up after FC already registered the uffd.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type accessedOffsetsIpc struct {
	otherPid int
	f        *os.File
}

func (a *accessedOffsetsIpc) Offsets(ctx context.Context) ([]uint, error) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGUSR2)

	syscall.Kill(a.otherPid, syscall.SIGUSR2)

	select {
	case <-sigc:
		fmt.Fprintf(os.Stdout, "received signal to read offsets\n")
	case <-ctx.Done():
		return nil, fmt.Errorf("context done: %w", ctx.Err())
	}

	// Rewind before reading
	if _, err := a.f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek failed: %w", err)
	}

	offsetsBytes, err := io.ReadAll(a.f)
	if err != nil {
		return nil, fmt.Errorf("failed to read offsets: %w", err)
	}

	// decode the offsets
	offsets := []uint{}
	for _, offset := range offsetsBytes {
		offsets = append(offsets, uint(offset))
	}

	return offsets, nil
}

func (a *accessedOffsetsIpc) Close() error {
	return a.f.Close()
}

func (a *accessedOffsetsIpc) Write(ctx context.Context, offsets []uint) error {
	if err := a.f.Truncate(0); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	if _, err := a.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek failed: %w", err)
	}

	offsetsBytes := []byte{}
	for _, offset := range offsets {
		offsetsBytes = append(offsetsBytes, byte(offset))
	}

	_, err := a.f.Write(offsetsBytes)
	if err != nil {
		return fmt.Errorf("failed to write offsets: %w", err)
	}

	err = a.f.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync offsets: %w", err)
	}

	// print file name
	fmt.Fprintf(os.Stdout, "sending signal to other process to read offsets\n")

	time.Sleep(1 * time.Second)

	syscall.Kill(a.otherPid, syscall.SIGUSR2)

	return nil
}

// Main process, FC in our case
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, func()) {
	t.Helper()

	cleanupList := []func(){}

	cleanup := func() {
		slices.Reverse(cleanupList)

		for _, cleanup := range cleanupList {
			cleanup()
		}
	}

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
		unmap()
	})

	uffd, err := userfaultfd.NewUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
		userfaultfd.Close(uffd)
	})

	err = userfaultfd.ConfigureApi(uffd, tt.pagesize)
	require.NoError(t, err)

	err = userfaultfd.Register(uffd, memoryStart, uint64(size), userfaultfd.UFFDIO_REGISTER_MODE_MISSING)
	require.NoError(t, err)

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperServingProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", memoryStart))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_PAGE_SIZE=%d", tt.pagesize))

	// Passing the fd to the child process
	uffdFile := os.NewFile(uintptr(uffd), "userfaultfd")

	contentFile, err := os.CreateTemp(os.TempDir(), "content-*.txt")
	require.NoError(t, err)

	// Write content to the file before passing it to child
	_, err = contentFile.Write(data.Content())
	require.NoError(t, err)

	// Seek to beginning so child can read from start
	_, err = contentFile.Seek(0, 0)
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
		contentFile.Close()
		os.Remove(contentFile.Name())
	})

	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_CONTENT_FILE=%s", contentFile.Name()))

	missingRequestsFile, err := os.CreateTemp(os.TempDir(), "missing-requests-*.txt")
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
		missingRequestsFile.Close()
		os.Remove(missingRequestsFile.Name())
	})

	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MISSING_REQUESTS_FILE=%s", missingRequestsFile.Name()))

	cmd.ExtraFiles = []*os.File{
		uffdFile,
		contentFile,
		missingRequestsFile,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	servingProcessReady := make(chan os.Signal, 1)
	signal.Notify(servingProcessReady, syscall.SIGUSR1)

	err = cmd.Start()
	require.NoError(t, err)

	go func() {
		err := cmd.Wait()
		assert.NoError(t, err)
	}()

	cleanupList = append(cleanupList, func() {
		cmd.Process.Signal(syscall.SIGTERM)
	})

	select {
	case <-servingProcessReady:
		fmt.Println("child signaled ready")
	case <-t.Context().Done():
		cleanup()

		return nil, nil
	}

	return &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		uffd:       uffd,
		accessed:   &accessedOffsetsIpc{otherPid: cmd.Process.Pid, f: missingRequestsFile},
	}, cleanup
}

// Secondary process, orchestrator in our case
func TestHelperServingProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	startRaw, err := strconv.Atoi(os.Getenv("GO_MMAP_START"))
	if err != nil {
		fmt.Print("exit parsing mmap start", err)
		os.Exit(1)
	}

	memoryStart := uintptr(startRaw)

	uffdFile := os.NewFile(uintptr(3), "userfaultfd")
	defer uffdFile.Close()

	uffd := uffdFile.Fd()

	contentFile := os.NewFile(uintptr(4), os.Getenv("GO_CONTENT_FILE"))
	defer contentFile.Close()

	missingRequestsFile := os.NewFile(uintptr(5), os.Getenv("GO_MISSING_REQUESTS_FILE"))
	defer missingRequestsFile.Close()

	fdExit, err := fdexit.New()
	if err != nil {
		fmt.Print("exit creating fd exit", err)
		os.Exit(1)
	}
	defer fdExit.Close()

	ppid := os.Getppid()

	accessedOffsets := &accessedOffsetsIpc{otherPid: ppid, f: missingRequestsFile}

	missingRequests := &sync.Map{}

	accessedOffsetsMap := &accessedOffsetsMap{missingRequests: missingRequests}

	missingRequestsSignal := make(chan os.Signal, 1)
	signal.Notify(missingRequestsSignal, syscall.SIGUSR2)

	go func() {
		for {
			select {
			case <-missingRequestsSignal:
				offsets, err := accessedOffsetsMap.Offsets(t.Context())
				if err != nil {
					fmt.Println("exit getting offsets", err)
					os.Exit(1)
				}

				accessedOffsets.Write(t.Context(), offsets)
				// Write accessed offsets to the file
			case <-t.Context().Done():
				return
			}
		}
	}()

	// Read directly from the file descriptor, not using Name()
	content, err := io.ReadAll(contentFile)
	if err != nil {
		fmt.Println("exit reading content", err)
		os.Exit(1)
	}

	pageSize, err := strconv.Atoi(os.Getenv("GO_MMAP_PAGE_SIZE"))
	if err != nil {
		fmt.Println("exit parsing page size", err)
		os.Exit(1)
	}

	data := testutils.NewMemorySlicer(content, int64(pageSize))

	m := mapping.FcMappings([]mapping.GuestRegionUffdMapping{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(len(content)),
			Offset:           0,
			PageSize:         uintptr(pageSize),
		},
	})

	exitUffd := make(chan struct{}, 1)

	go func() {
		err := Serve(t.Context(), int(uffd), m, data, fdExit, missingRequests, zap.L())
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}

		exitUffd <- struct{}{}
	}()

	cleanup := func() {
		signalExitErr := fdExit.SignalExit()
		assert.NoError(t, signalExitErr)

		<-exitUffd
	}

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGTERM)

	// Signalize ready
	syscall.Kill(ppid, syscall.SIGUSR1)

	select {
	case <-exitSignal:
	case <-t.Context().Done():
	}

	cleanup()

	os.Exit(0)
}
