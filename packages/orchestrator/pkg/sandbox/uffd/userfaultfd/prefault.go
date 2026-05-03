package userfaultfd

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

// Prefault proactively copies a page to guest memory at the given offset
// to speed up sandbox starts. EEXIST (already mapped) is handled gracefully.
func (u *Userfaultfd) Prefault(ctx context.Context, offset int64, data []byte) error {
	u.settleRequests.RLock()
	defer u.settleRequests.RUnlock()

	ctx, span := tracer.Start(ctx, "prefault page")
	defer span.End()

	addr, err := u.ma.GetHostVirtAddr(offset)
	if err != nil {
		return fmt.Errorf("failed to get host virtual address: %w", err)
	}

	if len(data) != int(u.pageSize) {
		return fmt.Errorf("data length (%d) does not match pagesize (%d)", len(data), u.pageSize)
	}

	// Skip already faulted or removed pages (madvise'd by FC).
	state := u.pageTracker.get(addr)
	if state == faulted || state == removed {
		return nil
	}

	// Prefault as a read so the page gets WP set. If a concurrent on-demand
	// write fault beats us, faultPage returns EEXIST and we skip setState.
	handled, err := u.faultPage(
		ctx,
		addr,
		offset,
		block.Read,
		directDataSource{data, int64(u.pageSize)},
		nil,
	)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("failed to fault page: %w", err)
	}

	if !handled {
		span.AddEvent("prefault: page already faulted or write returned EAGAIN")
	} else {
		u.pageTracker.setState(addr, addr+u.pageSize, faulted)
		u.prefetchTracker.Add(offset, block.Prefetch)
	}

	return nil
}

// directDataSource wraps a byte slice to implement block.Slicer for prefaulting.
type directDataSource struct {
	data     []byte
	pagesize int64
}

func (d directDataSource) Slice(_ context.Context, _, _ int64) ([]byte, error) {
	return d.data, nil
}

func (d directDataSource) BlockSize() int64 {
	return d.pagesize
}
