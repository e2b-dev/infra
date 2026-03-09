package userfaultfd

import (
	"context"
	"fmt"
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

	handled, err := u.faultPage(
		ctx,
		addr,
		offset,
		directDataSource{data, int64(u.pageSize)},
		nil,
		UFFDIO_COPY_MODE_WP,
	)
	if err != nil {
		return fmt.Errorf("failed to fault page: %w", err)
	}

	if !handled {
		span.RecordError(fmt.Errorf("page already faulted"))
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
