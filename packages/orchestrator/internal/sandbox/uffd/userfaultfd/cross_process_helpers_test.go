package userfaultfd

// This tests is creating uffd in the main process and handling the page faults in another process.
// It prevents problems with Go mmap during testing (https://pojntfx.github.io/networked-linux-memsync/main.html#limitations) and also more accurately simulates what we do with Firecracker.
// These problems are not affecting Firecracker, because:
// 1. It is a different process that handles the page faults
// 2. Does not use garbage collection

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// MemorySlicer exposes byte slice via the Slicer interface.
// This is used for testing purposes.
type MemorySlicer struct {
	content  []byte
	pagesize int64
}

var _ block.Slicer = (*MemorySlicer)(nil)

func NewMemorySlicer(content []byte, pagesize int64) *MemorySlicer {
	return &MemorySlicer{
		content:  content,
		pagesize: pagesize,
	}
}

func (s *MemorySlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func (s *MemorySlicer) Size() (int64, error) {
	return int64(len(s.content)), nil
}

func (s *MemorySlicer) Content() []byte {
	return s.content
}

func (s *MemorySlicer) BlockSize() int64 {
	return s.pagesize
}

func RandomPages(pagesize, numberOfPages uint64) *MemorySlicer {
	size := pagesize * numberOfPages

	n := int(size)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return NewMemorySlicer(buf, int64(pagesize))
}

// Main process, FC in our case
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := RandomPages(tt.pagesize, tt.numberOfPages)

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

	err = register(uffdFd, memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperServingProcess")
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

	offsetsReader, offsetsWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		offsetsReader.Close()
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
		offsetsWriter,
		readyWriter,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	contentReader.Close()
	offsetsWriter.Close()
	readyWriter.Close()
	uffdFile.Close()

	t.Cleanup(func() {
		signalErr := cmd.Process.Signal(syscall.SIGUSR1)
		assert.NoError(t, signalErr)

		waitErr := cmd.Wait()
		// It can be either nil, an ExitError, a context.Canceled error, or "signal: killed"
		assert.True(t,
			(waitErr != nil && func(err error) bool {
				var exitErr *exec.ExitError

				return errors.As(err, &exitErr)
			}(waitErr)) ||
				errors.Is(waitErr, context.Canceled) ||
				(waitErr != nil && strings.Contains(waitErr.Error(), "signal: killed")) ||
				waitErr == nil,
			"unexpected error: %v", waitErr,
		)
	})

	offsetsOnce := func() ([]uint, error) {
		err := cmd.Process.Signal(syscall.SIGUSR2)
		if err != nil {
			return nil, err
		}

		offsetsBytes, err := io.ReadAll(offsetsReader)
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
		memoryArea:  &memoryArea,
		pagesize:    tt.pagesize,
		data:        data,
		offsetsOnce: offsetsOnce,
	}, nil
}

// Secondary process, orchestrator in our case
func TestHelperServingProcess(t *testing.T) {
	t.Parallel()

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

	data := NewMemorySlicer(content, pageSize)

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

	uffd, err := NewUserfaultfdFromFd(uffdFd, data, m, l)
	if err != nil {
		return fmt.Errorf("exit creating uffd: %w", err)
	}

	offsetsFile := os.NewFile(uintptr(5), "offsets")

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
				for offset := range uffd.Dirty().Offsets() {
					writeErr := binary.Write(offsetsFile, binary.LittleEndian, uint64(offset))
					if writeErr != nil {
						msg := fmt.Errorf("error writing offsets to file: %w", writeErr)

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
		if serverErr != nil {
			msg := fmt.Errorf("error serving: %w", serverErr)

			fmt.Fprint(os.Stderr, msg.Error())

			cancel(msg)

			return
		}
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
	signal.Notify(exitSignal, syscall.SIGUSR1)
	defer signal.Stop(exitSignal)

	readyFile := os.NewFile(uintptr(6), "ready")

	closeErr := readyFile.Close()
	if closeErr != nil {
		return fmt.Errorf("error closing ready file: %w", closeErr)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("context done: %w: %w", ctx.Err(), context.Cause(ctx))
	case <-exitSignal:
		return nil
	}
}
