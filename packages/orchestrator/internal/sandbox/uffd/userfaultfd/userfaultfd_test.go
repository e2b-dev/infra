package userfaultfd

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type dummySlicer struct {
	blockSize int64
}

func (d *dummySlicer) Slice(_ context.Context, _ int64, length int64) ([]byte, error) {
	return make([]byte, length), nil
}

func (d *dummySlicer) BlockSize() int64 {
	return d.blockSize
}

// TestServeExitsOnClosedUffd tests that Serve exits when the uffd fd is closed
// but exitfd is NOT signaled. Without proper handling, this causes a busy-loop.
func TestServeExitsOnClosedUffd(t *testing.T) {
	t.Parallel()

	pageSize := uint64(header.PageSize)
	numPages := uint64(1)
	size := pageSize * numPages

	_, memoryStart, err := testutils.NewPageMmap(t, size, pageSize)
	require.NoError(t, err)

	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)

	err = configureApi(uffdFd, pageSize)
	require.NoError(t, err)

	err = register(uffdFd, memoryStart, size, UFFDIO_REGISTER_MODE_MISSING)
	require.NoError(t, err)

	m := memory.NewMapping([]memory.Region{{
		BaseHostVirtAddr: memoryStart,
		Offset:           0,
		Size:             uintptr(size),
		PageSize:         uintptr(pageSize),
	}})

	u, err := NewUserfaultfdFromFd(uintptr(uffdFd), &dummySlicer{blockSize: int64(pageSize)}, m, logger.L())
	require.NoError(t, err)

	fdExit, err := fdexit.New()
	require.NoError(t, err)
	defer fdExit.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- u.Serve(t.Context(), fdExit)
	}()

	time.Sleep(50 * time.Millisecond)

	// Close uffd fd without signaling exitfd - this should trigger exit, not busy-loop
	require.NoError(t, u.Close())

	start := time.Now()
	select {
	case err := <-errCh:
		elapsed := time.Since(start)
		require.Less(t, elapsed, 100*time.Millisecond, "Serve should exit quickly, not spin")
		t.Logf("✓ Serve exited in %v (err: %v)", elapsed, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve stuck in busy-loop after uffd fd closed")
	}
}
