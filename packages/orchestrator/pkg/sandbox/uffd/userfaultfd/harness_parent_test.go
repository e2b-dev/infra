package userfaultfd

// Parent side of the cross-process UFFD test harness. The parent
// owns the userfaultfd (created and registered against an mmap that
// lives in the parent's address space) and drives the in-VM page
// fault servicing logic from a child helper process. We re-exec the
// test binary as the child so the actual page-fault handling runs
// in a process where we can fully control memory layout (no Go GC
// scanning / touching the registered region) — which mirrors how
// Firecracker uses UFFD in production.
//
// The side channels between parent and child are intentionally
// minimal: a single env var flagging the child as the helper, an
// env var carrying the rendezvous socket path, the userfaultfd
// itself handed off via ExtraFiles (it's a kernel object, has to go
// through that), and the unix domain socket used for JSON-RPC. All
// configuration (mmap geometry, source content, feature toggles)
// flows over a single Lifecycle.Bootstrap RPC.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
)

// MemorySlicer exposes a byte slice via the Slicer interface.
// Test-only.
type MemorySlicer struct {
	content  []byte
	pagesize int64
}

var _ block.Slicer = (*MemorySlicer)(nil)

func NewMemorySlicer(content []byte, pagesize int64) *MemorySlicer {
	return &MemorySlicer{content: content, pagesize: pagesize}
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
	buf := make([]byte, int(size))
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return NewMemorySlicer(buf, int64(pagesize))
}

// Env vars used by the child helper process. The set is deliberately
// small: anything else lives in BootstrapArgs and flows over RPC.
const (
	envHelperFlag = "GO_TEST_HELPER_PROCESS"
	envSocketPath = "GO_UFFD_SOCKET"
)

// configureCrossProcessTest spawns the helper child, hands it the
// userfaultfd, and drives initial setup via Lifecycle.Bootstrap.
// All subsequent test interaction goes through the returned
// testHandler's *harnessClient.
func configureCrossProcessTest(ctx context.Context, t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), tt.pagesize)
	require.NoError(t, err)

	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)
	t.Cleanup(func() {
		uffdFd.close()
	})

	require.NoError(t, configureApi(uffdFd, tt.pagesize))
	require.NoError(t, register(uffdFd, memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))

	socketPath := filepath.Join(t.TempDir(), "rpc.sock")
	listenCfg := net.ListenConfig{}
	listener, err := listenCfg.Listen(ctx, "unix", socketPath)
	require.NoError(t, err)

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperServingProcess", "-test.timeout=0")
	cmd.Env = append(os.Environ(),
		envHelperFlag+"=1",
		envSocketPath+"="+socketPath,
	)

	// F_DUPFD_CLOEXEC duplicates uffdFd into a new fd that is born
	// with CLOEXEC set, atomically. The previous incarnation used
	// syscall.Dup followed by F_SETFD, which left a brief window
	// where the dup'd fd was visible without CLOEXEC; under
	// -parallel that window let other concurrent forks inherit a
	// uffd they did not own and produced hard-to-diagnose,
	// parallel-only deadlocks. Removing the window removes the
	// need for the childForkMu serialising lock the previous
	// version used to wrap cmd.Start.
	dup, err := unix.FcntlInt(uintptr(uffdFd), unix.F_DUPFD_CLOEXEC, 0)
	require.NoError(t, err)

	uffdFile := os.NewFile(uintptr(dup), "uffd")
	cmd.ExtraFiles = []*os.File{uffdFile}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	startErr := cmd.Start()
	uffdFile.Close()
	require.NoError(t, startErr)

	// Accept the child's connection. Tight deadline so a wedged
	// child surfaces fast instead of hanging the test.
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := listener.Accept()
		acceptCh <- acceptResult{conn: c, err: err}
	}()

	var conn net.Conn
	select {
	case res := <-acceptCh:
		require.NoError(t, res.err)
		conn = res.conn
	case <-time.After(10 * time.Second):
		listener.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()

		return nil, errors.New("child did not connect within 10s")
	}
	listener.Close()

	client := newHarnessClient(conn, cmd)

	h := &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		client:     client,
	}

	if err := client.Bootstrap(BootstrapArgs{
		MmapStart: uint64(memoryStart),
		Pagesize:  int64(tt.pagesize),
		TotalSize: size,
		AlwaysWP:  tt.alwaysWP,
		Barriers:  tt.barriers,
		Content:   data.Content(),
	}); err != nil {
		return nil, fmt.Errorf("Lifecycle.Bootstrap: %w", err)
	}

	// WaitReady's successful reply IS the readiness signal. Bootstrap
	// is synchronous, so today this is largely a smoke check, but
	// the explicit RPC is kept so that future async-Bootstrap variants
	// can hold the parent here without breaking the call site.
	if err := client.WaitReady(); err != nil {
		return nil, fmt.Errorf("Lifecycle.WaitReady: %w", err)
	}

	t.Cleanup(func() {
		// Best-effort graceful shutdown via RPC. If the child has
		// already crashed the RPC will error and we fall back to
		// killing the process below.
		_ = client.Shutdown()
		_ = client.Close()

		waitErr := cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				t.Logf("helper process Wait: %v", waitErr)
			}
		}

		// Tear down the UFFD registration before the early uffdFd.close()
		// cleanup runs. Today this is a no-op (no test enables
		// UFFD_FEATURE_EVENT_REMOVE) but a follow-up that does will
		// otherwise see munmap block on un-acked REMOVE events queued
		// against the still-registered range. Cleanups run LIFO, so
		// this fires before the close registered earlier.
		assert.NoError(t, unregister(uffdFd, memoryStart, uint64(size)))
	})

	return h, nil
}
