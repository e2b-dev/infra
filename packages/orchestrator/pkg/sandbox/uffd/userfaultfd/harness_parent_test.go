package userfaultfd

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd/internal/rpcharness"
)

// MemorySlicer exposes a byte slice via the Slicer interface.
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

const envHelperFlag = "GO_TEST_HELPER_PROCESS"

func configureCrossProcessTest(ctx context.Context, t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), tt.pagesize)
	require.NoError(t, err)

	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)

	require.NoError(t, configureApi(uffdFd, tt.pagesize))
	require.NoError(t, register(uffdFd, memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperServingProcess", "-test.timeout=0")
	cmd.Env = append(os.Environ(), envHelperFlag+"=1")

	// F_DUPFD_CLOEXEC dup's atomically with CLOEXEC set, so a concurrent
	// fork in another goroutine cannot inherit the dup'd fd before we
	// hand it off via ExtraFiles.
	dup, err := unix.FcntlInt(uintptr(uffdFd), unix.F_DUPFD_CLOEXEC, 0)
	require.NoError(t, err)
	uffdFile := os.NewFile(uintptr(dup), "uffd")

	// Socketpair gives a connected AF_UNIX pair in one syscall, with both
	// ends born CLOEXEC; cmd.ExtraFiles clears CLOEXEC on the child end
	// inside ForkExec.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	require.NoError(t, err)
	parentEnd := os.NewFile(uintptr(fds[0]), "rpc-parent")
	childEnd := os.NewFile(uintptr(fds[1]), "rpc-child")

	cmd.ExtraFiles = []*os.File{uffdFile, childEnd}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	startErr := cmd.Start()
	uffdFile.Close()
	childEnd.Close()
	if startErr != nil {
		parentEnd.Close()
		require.NoError(t, startErr)
	}

	// FileConn dups the underlying fd; close parentEnd to avoid leaking it.
	parentConn, err := net.FileConn(parentEnd)
	parentEnd.Close()
	require.NoError(t, err)

	client := rpcharness.NewClient(parentConn)

	t.Cleanup(func() {
		_ = client.Shutdown()
		_ = client.Close()

		waitErr := cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				t.Logf("helper process Wait: %v", waitErr)
			}
		}

		// Unregister before close so a future test that enables
		// UFFD_FEATURE_EVENT_REMOVE does not see munmap block on
		// un-acked REMOVE events queued against the registered range.
		assert.NoError(t, unregister(uffdFd, memoryStart, uint64(size)))
		uffdFd.close()
	})

	h := &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		client:     client,
	}

	if err := client.Bootstrap(rpcharness.BootstrapArgs{
		MmapStart: uint64(memoryStart),
		Pagesize:  int64(tt.pagesize),
		TotalSize: size,
		AlwaysWP:  tt.alwaysWP,
		Barriers:  tt.barriers,
		Content:   data.Content(),
	}); err != nil {
		return nil, fmt.Errorf("Lifecycle.Bootstrap: %w", err)
	}

	// Bootstrap is synchronous, so its reply already implies readiness;
	// WaitReady is kept as a separate RPC so an async-Bootstrap variant
	// can hold the parent here without touching call sites.
	if err := client.WaitReady(); err != nil {
		return nil, fmt.Errorf("Lifecycle.WaitReady: %w", err)
	}

	return h, nil
}
