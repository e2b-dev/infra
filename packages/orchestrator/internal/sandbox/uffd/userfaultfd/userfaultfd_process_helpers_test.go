package userfaultfd

// This tests is creating uffd in the main process and handling the page faults in another process.
// It prevents problems with Go mmap during testing (https://pojntfx.github.io/networked-linux-memsync/main.html#limitations) and also more accurately simulates what we do with Firecracker.
// These problems are not affecting Firecracker, because:
// 1. It is a different process that handles the page faults
// 2. Does not use garbage collection

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Main process, FC in our case
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), tt.pagesize)
	require.NoError(t, err)

	// We can pass mapping nil as the serve is used only in the helper process.
	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)

	t.Cleanup(func() {
		uffdFd.close()
	})

	err = configureApi(uffdFd, tt.pagesize)
	require.NoError(t, err)

	err = uffdFd.register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
	require.NoError(t, err)

	// We don't use t.Context() here, because we want to be able to kill the process manually and listen to the correct exit code,
	// while also handling the cleanup of the uffd. The t.Context seems to trigger before the test cleanup is started.
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestHelperServingProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", memoryStart))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_PAGE_SIZE=%d", tt.pagesize))

	dup, err := syscall.Dup(int(uffdFd))
	require.NoError(t, err)

	// clear FD_CLOEXEC on the dup we pass across exec
	_, err = unix.FcntlInt(uintptr(dup), unix.F_SETFD, 0)
	require.NoError(t, err)

	uffdFile := os.NewFile(uintptr(dup), "uffd")

	contentReader, contentWriter, err := os.Pipe()
	require.NoError(t, err)

	go func() {
		_, writeErr := contentWriter.Write(data.Content())
		assert.NoError(t, writeErr)

		closeErr := contentWriter.Close()
		assert.NoError(t, closeErr)
	}()

	accessedOffsetsReader, accessedOffsetsWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		accessedOffsetsReader.Close()
	})

	dirtyOffsetsReader, dirtyOffsetsWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		dirtyOffsetsReader.Close()
	})

	readyReader, readyWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		readyReader.Close()
	})

	readySignal := make(chan struct{}, 1)
	go func() {
		_, err := io.ReadAll(readyReader)
		assert.NoError(t, err)

		readySignal <- struct{}{}
	}()

	cmd.ExtraFiles = []*os.File{
		uffdFile,
		contentReader,
		accessedOffsetsWriter,
		readyWriter,
		dirtyOffsetsWriter,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	contentReader.Close()
	accessedOffsetsWriter.Close()
	readyWriter.Close()
	uffdFile.Close()
	dirtyOffsetsWriter.Close()

	go func() {
		waitErr := cmd.Wait()
		assert.NoError(t, waitErr)

		assert.NotEqual(t, -1, cmd.ProcessState.ExitCode(), "process was not terminated gracefully")
		assert.NotEqual(t, 2, cmd.ProcessState.ExitCode(), "fd exit prematurely terminated the serve loop")
		assert.NotEqual(t, 1, cmd.ProcessState.ExitCode(), "process exited with unexpected exit code")

		assert.Equal(t, 0, cmd.ProcessState.ExitCode())
	}()

	t.Cleanup(func() {
		// We are using SIGHUP to actually get exit code, not -1.
		signalErr := cmd.Process.Signal(syscall.SIGTERM)
		assert.NoError(t, signalErr)
	})

	accessedOffsetsOnce := func() ([]uint, error) {
		err := cmd.Process.Signal(syscall.SIGUSR2)
		if err != nil {
			return nil, err
		}

		offsetsBytes, err := io.ReadAll(accessedOffsetsReader)
		if err != nil {
			return nil, err
		}

		var offsetList []uint

		if len(offsetsBytes)%8 != 0 {
			return nil, fmt.Errorf("invalid offsets bytes length: %d", len(offsetsBytes))
		}

		for i := 0; i < len(offsetsBytes); i += 8 {
			offsetList = append(offsetList, uint(binary.LittleEndian.Uint64(offsetsBytes[i:i+8])))
		}

		return offsetList, nil
	}

	dirtyOffsetsOnce := func() ([]uint, error) {
		err := cmd.Process.Signal(syscall.SIGUSR1)
		if err != nil {
			return nil, err
		}

		offsetsBytes, err := io.ReadAll(dirtyOffsetsReader)
		if err != nil {
			return nil, err
		}

		var offsetList []uint

		if len(offsetsBytes)%8 != 0 {
			return nil, fmt.Errorf("invalid offsets bytes length: %d", len(offsetsBytes))
		}

		for i := 0; i < len(offsetsBytes); i += 8 {
			offsetList = append(offsetList, uint(binary.LittleEndian.Uint64(offsetsBytes[i:i+8])))
		}

		return offsetList, nil
	}

	select {
	case <-t.Context().Done():
		return nil, t.Context().Err()
	case <-readySignal:
	}

	return &testHandler{
		memoryArea:          &memoryArea,
		pagesize:            tt.pagesize,
		data:                data,
		accessedOffsetsOnce: accessedOffsetsOnce,
		dirtyOffsetsOnce:    dirtyOffsetsOnce,
	}, nil
}

// Secondary process, orchestrator in our case.
func TestHelperServingProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	err := crossProcessServe()
	if errors.Is(err, fdexit.ErrFdExit) {
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error serving: %v", err)
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

	uffdFd := uffdFile.Fd()

	contentFile := os.NewFile(uintptr(4), "content")
	defer contentFile.Close()

	content, err := io.ReadAll(contentFile)
	if err != nil {
		return fmt.Errorf("exit reading content: %w", err)
	}

	pageSize, err := strconv.ParseInt(os.Getenv("GO_MMAP_PAGE_SIZE"), 10, 64)
	if err != nil {
		return fmt.Errorf("exit parsing page size: %w", err)
	}

	data := testutils.NewMemorySlicer(content, pageSize)

	m := memory.NewMapping([]memory.Region{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(len(content)),
			Offset:           0,
			PageSize:         uintptr(pageSize),
		},
	})

	exitUffd := make(chan struct{}, 1)
	defer close(exitUffd)

	l, err := logger.NewDevelopmentLogger()
	if err != nil {
		return fmt.Errorf("exit creating logger: %w", err)
	}

	uffd, err := NewUserfaultfdFromFd(Fd(uffdFd), data, m, logger)
	if err != nil {
		return fmt.Errorf("exit creating uffd: %w", err)
	}

	accessedOffsetsFile := os.NewFile(uintptr(5), "accessed-offsets")

	accessedOffsestsSignal := make(chan os.Signal, 1)
	signal.Notify(accessedOffsestsSignal, syscall.SIGUSR2)
	defer signal.Stop(accessedOffsestsSignal)

	go func() {
		defer accessedOffsetsFile.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case <-accessedOffsestsSignal:
				for offset := range accessed(uffd).Offsets() {
					writeErr := binary.Write(accessedOffsetsFile, binary.LittleEndian, uint64(offset))
					if writeErr != nil {
						msg := fmt.Errorf("error writing accessed offsets to file: %w", writeErr)

						fmt.Fprint(os.Stderr, msg.Error())

						cancel(msg)

						return
					}
				}

				return
			}
		}
	}()

	dirtyOffsetsFile := os.NewFile(uintptr(7), "dirty-offsets")

	dirtyOffsetsSignal := make(chan os.Signal, 1)
	signal.Notify(dirtyOffsetsSignal, syscall.SIGUSR1)
	defer signal.Stop(dirtyOffsetsSignal)

	go func() {
		defer dirtyOffsetsFile.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case <-dirtyOffsetsSignal:
				for offset := range uffd.Dirty().Offsets() {
					writeErr := binary.Write(dirtyOffsetsFile, binary.LittleEndian, uint64(offset))
					if writeErr != nil {
						msg := fmt.Errorf("error writing dirty offsets to file: %w", writeErr)

						fmt.Fprint(os.Stderr, msg.Error())

						cancel(msg)

						return
					}
				}

				return
			}
		}
	}()

	fdExit, err := fdexit.New()
	if err != nil {
		return fmt.Errorf("exit creating fd exit: %w", err)
	}
	defer fdExit.Close()

	go func() {
		defer func() {
			exitUffd <- struct{}{}
		}()

		serverErr := uffd.Serve(ctx, fdExit)
		if errors.Is(serverErr, fdexit.ErrFdExit) {
			err := fmt.Errorf("serving finished via fd exit: %w", serverErr)

			cancel(err)

			return
		}

		if serverErr != nil {
			msg := fmt.Errorf("error serving: %w", serverErr)

			fmt.Fprint(os.Stderr, msg.Error())

			cancel(msg)

			return
		}

		fmt.Fprint(os.Stderr, "serving finished")
	}()

	cleanup := func() {
		err := fdExit.SignalExit()
		if err != nil {
			msg := fmt.Errorf("error signaling exit: %w", err)

			fmt.Fprint(os.Stderr, msg.Error())

			cancel(msg)

			return
		}

		<-exitUffd
	}

	defer cleanup()

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGTERM)
	defer signal.Stop(exitSignal)

	readyFile := os.NewFile(uintptr(6), "ready")

	closeErr := readyFile.Close()
	if closeErr != nil {
		return fmt.Errorf("error closing ready file: %w", closeErr)
	}

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-exitSignal:
		return nil
	}
}
