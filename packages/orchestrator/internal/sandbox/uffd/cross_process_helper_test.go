package uffd

// This tests is creating uffd in a process and handling the page faults in another process.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
)

func getAccessedOffsets(missingRequests *sync.Map) ([]uint, error) {
	var offsets []uint

	missingRequests.Range(func(key, _ any) bool {
		offsets = append(offsets, uint(key.(int64)))

		return true
	})

	return offsets, nil
}

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

	var offsets []uint

	for i := 0; i < len(offsetsBytes); i += 8 {
		offsets = append(offsets, uint(binary.LittleEndian.Uint64(offsetsBytes[i:i+8])))
	}

	return offsets, nil
}

func (a *accessedOffsetsIpc) Close() error {
	return a.f.Close()
}

func (a *accessedOffsetsIpc) Write(offsets []uint) error {
	if err := a.f.Truncate(0); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	if _, err := a.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek failed: %w", err)
	}

	var offsetsBytes []byte

	for _, offset := range offsets {
		offsetsBytes = binary.LittleEndian.AppendUint64(offsetsBytes, uint64(offset))
	}

	_, err := a.f.Write(offsetsBytes)
	if err != nil {
		return fmt.Errorf("failed to write offsets: %w", err)
	}

	err = a.f.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync offsets: %w", err)
	}

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

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperServingProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", memoryStart))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_PAGE_SIZE=%d", tt.pagesize))

	// Passing the fd to the child process
	uffdFile := os.NewFile(uffd, "userfaultfd")

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
		// panic handler + print why
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic in wait cmd: %v\n", r)
				debug.PrintStack()
			}
		}()

		err := cmd.Wait()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error in wait cmd: %v\n", err)
			debug.PrintStack()
		}
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
		accessed:   accessedOffsetsIpc{otherPid: cmd.Process.Pid, f: missingRequestsFile},
	}, cleanup
}

// Secondary process, orchestrator in our case
func TestHelperServingProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	err := crossProcessServe()
	if err != nil {
		fmt.Println("exit serving process", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func crossProcessServe() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startRaw, err := strconv.Atoi(os.Getenv("GO_MMAP_START"))
	if err != nil {
		return fmt.Errorf("exit parsing mmap start: %w", err)
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
		return fmt.Errorf("exit creating fd exit: %w", err)
	}
	defer fdExit.Close()

	ppid := os.Getppid()

	accessedOffsets := &accessedOffsetsIpc{otherPid: ppid, f: missingRequestsFile}

	missingRequests := &sync.Map{}

	missingRequestsSignal := make(chan os.Signal, 1)
	signal.Notify(missingRequestsSignal, syscall.SIGUSR2)

	go func() {

		defer func() {
			if r := recover(); r != nil {
				fmt.Println("panic in missing requests process", r)
			}
		}()

		for {
			select {
			case <-missingRequestsSignal:
				offsets, err := getAccessedOffsets(missingRequests)
				if err != nil {
					fmt.Println("exit getting offsets", err)

					cancel()
				}

				accessedOffsets.Write(offsets)
				// Write accessed offsets to the file
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read directly from the file descriptor, not using Name()
	content, err := io.ReadAll(contentFile)
	if err != nil {
		return fmt.Errorf("exit reading content: %w", err)
	}

	pageSize, err := strconv.Atoi(os.Getenv("GO_MMAP_PAGE_SIZE"))
	if err != nil {
		return fmt.Errorf("exit parsing page size: %w", err)
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
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("panic in serving process", r)
			}
		}()

		err := Serve(ctx, int(uffd), m, data, fdExit, missingRequests, zap.L())
		if err != nil {
			fmt.Println("[TestUffdWriteProtect] failed to serve uffd", err)
		}

		exitUffd <- struct{}{}
	}()

	cleanup := func() {
		fdExit.SignalExit()

		<-exitUffd
	}
	defer cleanup()

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGTERM)

	// Signalize ready
	syscall.Kill(ppid, syscall.SIGUSR1)

	select {
	case <-exitSignal:
	case <-ctx.Done():
	}

	return nil
}
