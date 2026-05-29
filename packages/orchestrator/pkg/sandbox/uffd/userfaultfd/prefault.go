//go:build linux

package userfaultfd

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// Prefault proactively copies a page to guest memory at the given offset
// to speed up sandbox starts. EEXIST (already mapped) is handled gracefully.
func (u *Userfaultfd) Prefault(ctx context.Context, offset int64, data []byte) error {
	// Test hook: fires before settleRequests.RLock so that a test can park
	// the goroutine here, call Close() concurrently, then release and observe
	// that the closed check below returns nil without calling UFFDIO_COPY.
	if h := u.testFaultHook.Load(); h != nil {
		(*h)(0, faultPhaseBeforePrefaultRLock)
	}

	u.settleRequests.RLock()
	defer u.settleRequests.RUnlock()

	// Close() sets closed under Lock(). Seeing it here under RLock means the
	// fd is already closed and its number may have been recycled by the OS —
	// skip silently instead of calling UFFDIO_COPY and getting EBADF/ENOTTY.
	if u.closed {
		return nil
	}

	ctx, span := tracer.Start(ctx, "prefault page")
	defer span.End()

	addr, err := u.ma.GetHostVirtAddr(offset)
	if err != nil {
		return fmt.Errorf("failed to get host virtual address: %w", err)
	}

	if len(data) != int(u.pageSize) {
		return fmt.Errorf("data length (%d) does not match pagesize (%d)", len(data), u.pageSize)
	}

	idx := uint32(header.BlockIdx(offset, int64(u.pageSize)))
	state := u.pageTracker.Get(idx)
	if state == block.Dirty || state == block.Zero {
		return nil
	}

	// Prefault as a read so the page gets WP set. A concurrent on-demand
	// fault that installs the page first returns faultInstalled via EEXIST.
	outcome, err := u.faultPage(
		ctx,
		addr,
		offset,
		block.Read,
		directDataSource{data: data},
		nil,
	)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("failed to fault page: %w", err)
	}

	switch outcome {
	case faultInstalled:
		u.pageTracker.SetRange(idx, idx+1, block.Dirty)
		u.prefetchTracker.Add(offset, block.Prefetch)
	case faultDeferred:
		span.AddEvent("prefault: write returned EAGAIN")
	case faultDiscarded:
		span.AddEvent("prefault: discarded (process gone)")
	}

	return nil
}

// directDataSource wraps a single page's bytes; off is ignored because the
// caller hands us exactly the page contents.
type directDataSource struct {
	data []byte
}

func (d directDataSource) ReadAt(_ context.Context, p []byte, _ int64) (int, error) {
	return copy(p, d.data), nil
}
