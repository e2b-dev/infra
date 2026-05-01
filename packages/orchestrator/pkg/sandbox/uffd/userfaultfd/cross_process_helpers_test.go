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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
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

	err = register(uffdFd, memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperServingProcess", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_START=%d", memoryStart))
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_MMAP_PAGE_SIZE=%d", tt.pagesize))
	if tt.alwaysWP {
		cmd.Env = append(cmd.Env, "GO_ALWAYS_WP=1")
	}
	if tt.gated {
		cmd.Env = append(cmd.Env, "GO_GATED=1")
	}

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

	extraFiles := []*os.File{
		uffdFile,
		contentReader,
		offsetsWriter,
		readyWriter,
	}

	var gateCmdWriter *os.File
	var gateSyncReader *os.File
	if tt.gated {
		var gateCmdReader *os.File
		gateCmdReader, gateCmdWriter, err = os.Pipe()
		require.NoError(t, err)

		var gateSyncWriter *os.File
		gateSyncReader, gateSyncWriter, err = os.Pipe()
		require.NoError(t, err)

		t.Cleanup(func() {
			gateCmdWriter.Close()
			gateSyncReader.Close()
		})

		extraFiles = append(extraFiles, gateCmdReader)  // fd 7
		extraFiles = append(extraFiles, gateSyncWriter) // fd 8
	}

	cmd.ExtraFiles = extraFiles
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	contentReader.Close()
	offsetsWriter.Close()
	readyWriter.Close()
	uffdFile.Close()
	if tt.gated {
		extraFiles[4].Close() // gateCmdReader
		extraFiles[5].Close() // gateSyncWriter
	}

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

		// Tear down the UFFD registration before the early uffdFd.close()
		// cleanup runs. Today this is a no-op (no test enables
		// UFFD_FEATURE_EVENT_REMOVE) but a follow-up that does will
		// otherwise see munmap block on un-acked REMOVE events queued
		// against the still-registered range. Cleanups run LIFO, so
		// this fires before the close registered earlier.
		assert.NoError(t, unregister(uffdFd, memoryStart, uint64(size)))
	})

	// pageStatesOnce asks the serving process for a snapshot of its pageTracker
	// and decodes it into a per-state view. It can only be called once.
	pageStatesOnce := func() (handlerPageStates, error) {
		err := cmd.Process.Signal(syscall.SIGUSR2)
		if err != nil {
			return handlerPageStates{}, err
		}

		var result handlerPageStates

		for {
			var entry pageStateEntry

			// binary.Read uses the same field layout as binary.Write on
			// the producer side (sum of fixed-size fields, no struct
			// padding), so we never have to hard-code the wire size.
			err := binary.Read(offsetsReader, binary.LittleEndian, &entry)
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				return handlerPageStates{}, fmt.Errorf("decoding page state entry: %w", err)
			}

			if pageState(entry.State) == faulted {
				result.faulted = append(result.faulted, uint(entry.Offset))
			}
		}

		slices.Sort(result.faulted)

		return result, nil
	}

	select {
	case <-t.Context().Done():
		return nil, t.Context().Err()
	case <-readySignal:
	}

	h := &testHandler{
		memoryArea:     &memoryArea,
		pagesize:       tt.pagesize,
		data:           data,
		pageStatesOnce: pageStatesOnce,
	}

	if tt.gated {
		h.servePause = func() error {
			if _, err := gateCmdWriter.Write([]byte{'P'}); err != nil {
				return err
			}
			var buf [1]byte
			_, err := gateSyncReader.Read(buf[:])

			return err
		}
		h.serveResume = func() error {
			_, err := gateCmdWriter.Write([]byte{'R'})

			return err
		}
	}

	return h, nil
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

	if os.Getenv("GO_ALWAYS_WP") == "1" {
		uffd.defaultCopyMode = UFFDIO_COPY_MODE_WP
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
				entries, entriesErr := uffd.pageStateEntries()
				if entriesErr != nil {
					cancel(fmt.Errorf("error getting page state entries: %w", entriesErr))

					return
				}

				for _, entry := range entries {
					writeErr := binary.Write(offsetsFile, binary.LittleEndian, entry)
					if writeErr != nil {
						cancel(fmt.Errorf("error writing page state entry: %w", writeErr))

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

	// stopFn drains whichever Serve goroutine is currently running and
	// is reset to a no-op once it has run. This makes both pause-then-exit
	// (no resume in between) and pause-resume-pause-exit safe: every
	// caller — gated 'P', gated 'R' replacing the previous stop, and the
	// final defer below — sees a stop function that matches the goroutine
	// actually running, and never blocks on an already-drained channel.
	var (
		stopMu sync.Mutex
		stopFn = func() {
			err := fdExit.SignalExit()
			if err != nil {
				msg := fmt.Errorf("error signaling exit: %w", err)

				fmt.Fprint(os.Stderr, msg.Error())

				cancel(msg)

				return
			}

			<-exitUffd
		}
	)

	stopServe := func() {
		stopMu.Lock()
		fn := stopFn
		stopFn = func() {}
		stopMu.Unlock()

		fn()
	}

	defer stopServe()

	if os.Getenv("GO_GATED") == "1" {
		gateCmdFile := os.NewFile(uintptr(7), "gate-cmd")
		defer gateCmdFile.Close()

		gateSyncFile := os.NewFile(uintptr(8), "gate-sync")
		defer gateSyncFile.Close()

		startServe := func() {
			newExit, fdErr := fdexit.New()
			if fdErr != nil {
				cancel(fmt.Errorf("error creating fd exit: %w", fdErr))

				return
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				if err := uffd.Serve(ctx, newExit); err != nil {
					cancel(fmt.Errorf("error serving: %w", err))
				}
			}()

			stopMu.Lock()
			stopFn = func() {
				newExit.SignalExit()
				<-done
				newExit.Close()
			}
			stopMu.Unlock()
		}

		go func() {
			var buf [1]byte
			for {
				if _, err := gateCmdFile.Read(buf[:]); err != nil {
					return
				}

				switch buf[0] {
				case 'P':
					stopServe()
					gateSyncFile.Write([]byte{1})
				case 'R':
					startServe()
				}
			}
		}()
	}

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

// pageStateEntry is the wire format used between the main test process
// and the serving helper process. State is emitted as a single byte so it
// can be written directly with binary.Write and decoded on the other side.
type pageStateEntry struct {
	State  uint8
	Offset uint64
}

// pageStateEntries returns a snapshot of every tracked page and its state.
// It holds the settleRequests write lock so no in-flight faultPage worker
// can mutate the pageTracker while we iterate.
func (u *Userfaultfd) pageStateEntries() ([]pageStateEntry, error) {
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	entries := make([]pageStateEntry, 0, len(u.pageTracker.m))
	for addr, state := range u.pageTracker.m {
		offset, err := u.ma.GetOffset(addr)
		if err != nil {
			return nil, fmt.Errorf("address %#x not in mapping: %w", addr, err)
		}

		entries = append(entries, pageStateEntry{uint8(state), uint64(offset)})
	}

	return entries, nil
}
