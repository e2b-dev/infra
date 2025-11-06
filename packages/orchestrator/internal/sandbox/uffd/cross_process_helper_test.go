package uffd

// This tests is creating uffd in the main process and handling the page faults in another process.
// It prevents problems with Go mmap during testing (https://pojntfx.github.io/networked-linux-memsync/main.html#limitations) and also more accurately simulates what we do with Firecracker.
// These problems are not affecting Firecracker, because:
// 1. It is a different process that handles the page faults
// 2. Does not use garbage collection

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

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
	defer signal.Stop(sigc)

	syscall.Kill(a.otherPid, syscall.SIGUSR2)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context done: %w", ctx.Err())
	case <-sigc:
	}

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
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	if err != nil {
		return nil, err
	}

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		unmap()
	})

	uffd, err := userfaultfd.NewUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		userfaultfd.Close(uffd)
	})

	err = userfaultfd.ConfigureApi(uffd, tt.pagesize)
	if err != nil {
		return nil, err
	}

	err = userfaultfd.Register(uffd, memoryStart, uint64(size), userfaultfd.UFFDIO_REGISTER_MODE_MISSING)
	if err != nil {
		return nil, err
	}

	// t.Cleanup(func() {
	// 	// We explicitly unregister memory during cleanup, because as the test runs in the same process, we can get addresses that seems to collide
	// 	// and without synchronizing the uffd cleanup, the new memory could be served by the old uffd.
	// 	err := userfaultfd.Unregister(uffd, memoryStart, uint64(size))
	// 	assert.NoError(t, err)
	// })

	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestHelperServingProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", memoryStart))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_PAGE_SIZE=%d", tt.pagesize))

	dup, err := syscall.Dup(int(uffd))
	if err != nil {
		return nil, err
	}

	// clear FD_CLOEXEC on the dup we pass across exec
	if _, err := unix.FcntlInt(uintptr(dup), unix.F_SETFD, 0); err != nil {
		return nil, err
	}

	id := uuid.New().String()
	// Passing the fd to the child process
	uffdFile := os.NewFile(uintptr(dup), fmt.Sprintf("userfaultfd-%s", id))

	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_UFFD_FILE=%s", uffdFile.Name()))

	contentFile, err := os.CreateTemp(os.TempDir(), "content-*.txt")
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		err := contentFile.Close()
		assert.NoError(t, err)

		err = os.Remove(contentFile.Name())
		assert.NoError(t, err)
	})

	// Write content to the file before passing it to child
	_, err = contentFile.Write(data.Content())
	if err != nil {
		return nil, err
	}

	// Seek to beginning so child can read from start
	_, err = contentFile.Seek(0, 0)
	if err != nil {
		return nil, err
	}

	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_CONTENT_FILE=%s", contentFile.Name()))

	missingRequestsFile, err := os.CreateTemp(os.TempDir(), "missing-requests-*.txt")
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		err := missingRequestsFile.Close()
		assert.NoError(t, err)

		err = os.Remove(missingRequestsFile.Name())
		assert.NoError(t, err)
	})

	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MISSING_REQUESTS_FILE=%s", missingRequestsFile.Name()))

	cmd.ExtraFiles = []*os.File{
		uffdFile,
		contentFile,
		missingRequestsFile,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// We use SIGUSR1 to signal to main process that the serving process is ready to be used.
	servingProcessReady := make(chan os.Signal, 1)
	signal.Notify(servingProcessReady, syscall.SIGUSR1)
	defer signal.Stop(servingProcessReady)

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGUSR1)

		err := cmd.Wait()
		assert.NoError(t, err)
	})

	select {
	case <-t.Context().Done():
		return nil, t.Context().Err()
	case <-servingProcessReady:
	}

	return &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		uffd:       uffd,
		accessed:   accessedOffsetsIpc{otherPid: cmd.Process.Pid, f: missingRequestsFile},
	}, nil
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
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	startRaw, err := strconv.Atoi(os.Getenv("GO_MMAP_START"))
	if err != nil {
		return fmt.Errorf("exit parsing mmap start: %w", err)
	}

	memoryStart := uintptr(startRaw)

	uffdFile := os.NewFile(uintptr(3), os.Getenv("GO_UFFD_FILE"))
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

	// We use SIGUSR2 to signal to child to write accessed offsets to the file and to main process that they are ready to be read.
	missingRequestsSignal := make(chan os.Signal, 1)
	signal.Notify(missingRequestsSignal, syscall.SIGUSR2)
	defer signal.Stop(missingRequestsSignal)

	go func() {
		for {
			select {
			case <-missingRequestsSignal:
				offsets, err := getAccessedOffsets(missingRequests)
				if err != nil {
					cancel(err)

					return
				}

				accessedOffsets.Write(offsets)
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
	signal.Notify(exitSignal, syscall.SIGUSR1)

	// Signalize ready
	syscall.Kill(ppid, syscall.SIGUSR1)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exitSignal:
		return nil
	}
}
