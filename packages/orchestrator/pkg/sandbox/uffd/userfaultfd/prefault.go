package userfaultfd

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

// Prefault proactively copies a page to guest memory at the given offset.
// This is used to speed up sandbox starts by prefetching pages that are known to be needed.
// Returns nil on success, or if the page is already mapped (EEXIST is handled gracefully).
func (u *Userfaultfd) Prefault(ctx context.Context, offset int64, data []byte) error {
	ctx, span := tracer.Start(ctx, "prefault page")
	defer span.End()

	addr, err := u.ma.GetHostVirtAddr(offset)
	if err != nil {
		return fmt.Errorf("failed to get host virtual address: %w", err)
	}

	if len(data) != int(u.pageSize) {
		return fmt.Errorf("data length (%d) does not match pagesize (%d)", len(data), u.pageSize)
	}

	// We're treating prefault handling as if it was caused by a read access.
	// This way, we will fault the page with UFFD_COPY_MODE_WP which will set
	// the WP bit for the page. This works even in the case of a race with a
	// concurrent on-demand write access.
	//
	// If the on-demand fault handler beats us, we will get an EEXIST here.
	// If we beat the on-demand handler, it will get the EEXIST.
	//
	// In both cases, the WP bit will be cleared because it is handled asynchronously
	// by the kernel.
	handled, err := u.faultPage(
		ctx,
		addr,
		offset,
		block.Read,
		directDataSource{data, int64(u.pageSize)},
		nil,
	)
	if err != nil {
		span.RecordError(fmt.Errorf("could not prefault page"))

		return fmt.Errorf("failed to fault page: %w", err)
	}

	if !handled {
		span.AddEvent("prefault: page already faulted or write returned EAGAIN")
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
