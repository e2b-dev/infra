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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
)

// Main process, FC in our case
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	require.NoError(t, err)

	t.Cleanup(func() {
		unmap()
	})

	uffd, err := userfaultfd.NewUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)

	t.Cleanup(func() {
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

	dup, err := syscall.Dup(int(uffd))
	require.NoError(t, err)

	// clear FD_CLOEXEC on the dup we pass across exec
	_, err = unix.FcntlInt(uintptr(dup), unix.F_SETFD, 0)
	require.NoError(t, err)

	uffdFile := os.NewFile(uintptr(dup), "uffd")

	contentReader, contentWriter, err := os.Pipe()
	require.NoError(t, err)

	defer contentReader.Close()

	go func() {
		_, err := contentWriter.Write(data.Content())
		assert.NoError(t, err)

		err = contentWriter.Close()
		assert.NoError(t, err)
	}()

	offsetsReader, offsetsWriter, err := os.Pipe()
	require.NoError(t, err)

	defer offsetsWriter.Close()

	cmd.ExtraFiles = []*os.File{
		uffdFile,
		contentReader,
		offsetsWriter,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGUSR1)

		cmd.Wait()
	})

	offsetsOnce := func() ([]uint, error) {
		fmt.Fprintf(os.Stderr, "signaling offsets\n")

		err := cmd.Process.Signal(syscall.SIGUSR2)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(os.Stderr, "reading offsets\n")

		offsetsBytes, err := io.ReadAll(offsetsReader)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(os.Stderr, "offsets: %x\n", len(offsetsBytes))

		var offsetList []uint

		for i := 0; i < len(offsetsBytes); i += 8 {
			offsetList = append(offsetList, uint(binary.LittleEndian.Uint64(offsetsBytes[i:i+8])))
		}

		return offsetList, nil
	}

	return &testHandler{
		memoryArea:  &memoryArea,
		pagesize:    tt.pagesize,
		data:        data,
		uffd:        uffd,
		offsetsOnce: offsetsOnce,
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

	contentFile := os.NewFile(uintptr(4), "content")
	defer contentFile.Close()

	offsetsFile := os.NewFile(uintptr(5), "offsets")

	missingRequests := &sync.Map{}

	offsetsSignal := make(chan os.Signal, 1)
	signal.Notify(offsetsSignal, syscall.SIGUSR2)
	defer signal.Stop(offsetsSignal)

	go func() {
		defer offsetsFile.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case <-offsetsSignal:
				fmt.Fprintf(os.Stderr, "getting accessed offsets from cross process\n")
				offsets, err := getAccessedOffsets(missingRequests)
				if err != nil {
					cancel(err)
				}

				fmt.Fprintf(os.Stderr, "writing offsets to file: %x\n", len(offsets))

				for _, offset := range offsets {
					err = binary.Write(offsetsFile, binary.LittleEndian, uint64(offset))
					if err != nil {
						cancel(err)
					}
				}

				fmt.Fprintf(os.Stderr, "written offsets to file\n")

				return
			}
		}
	}()

	content, err := io.ReadAll(contentFile)
	if err != nil {
		return fmt.Errorf("exit reading content: %w", err)
	}

	fmt.Fprintf(os.Stderr, "content: %x\n", len(content))

	pageSize, err := strconv.Atoi(os.Getenv("GO_MMAP_PAGE_SIZE"))
	if err != nil {
		return fmt.Errorf("exit parsing page size: %w", err)
	}

	fmt.Fprintf(os.Stderr, "page size: %d\n", pageSize)

	data := testutils.NewMemorySlicer(content, int64(pageSize))

	m := mapping.FcMappings([]mapping.GuestRegionUffdMapping{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(len(content)),
			Offset:           0,
			PageSize:         uintptr(pageSize),
		},
	})

	fmt.Fprintf(os.Stderr, "memory start: %d\n", memoryStart)

	exitUffd := make(chan struct{}, 1)

	logger, err := zap.NewDevelopment()
	if err != nil {
		return fmt.Errorf("exit creating logger: %w", err)
	}

	fmt.Fprintf(os.Stderr, "creating fd exit\n")

	fdExit, err := fdexit.New()
	if err != nil {
		return fmt.Errorf("exit creating fd exit: %w", err)
	}
	defer fdExit.Close()

	fmt.Fprintf(os.Stderr, "serving\n")

	go func() {
		err = Serve(ctx, int(uffd), m, data, fdExit, missingRequests, logger)
		if err != nil {
			cancel(err)

			return
		}

		fmt.Fprintf(os.Stderr, "exit serving process\n")

		exitUffd <- struct{}{}
	}()

	fmt.Fprintf(os.Stderr, "waiting for exit\n")

	cleanup := func() {
		fdExit.SignalExit()

		<-exitUffd
	}

	defer cleanup()

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGUSR1)
	defer signal.Stop(exitSignal)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exitSignal:
		return nil
	}
}

func getAccessedOffsets(missingRequests *sync.Map) ([]uint, error) {
	var offsets []uint

	missingRequests.Range(func(key, _ any) bool {
		offsets = append(offsets, uint(key.(int64)))

		return true
	})

	return offsets, nil
}
