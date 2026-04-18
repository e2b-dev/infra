package userfaultfd

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

// Prefault proactively copies a page to guest memory at the given offset.
// This is used to speed up sandbox starts by prefetching pages that are known to be needed.
// Returns nil on success, or if the page is already mapped (EEXIST is handled gracefully).
func (u *Userfaultfd) Prefault(ctx context.Context, offset int64, data []byte) error {
	ctx, span := tracer.Start(ctx, "prefault page")
	defer span.End()

	// Get host virtual address and page size for this offset
	addr, pagesize, err := u.ma.GetHostVirtAddr(offset)
	if err != nil {
		return fmt.Errorf("failed to get host virtual address: %w", err)
	}

	if len(data) != int(pagesize) {
		return fmt.Errorf("data length (%d) is less than pagesize (%d)", len(data), pagesize)
	}

	return u.faultPage(ctx, addr, offset, pagesize, directDataSource{data, int64(pagesize)}, nil, block.Prefetch)
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
