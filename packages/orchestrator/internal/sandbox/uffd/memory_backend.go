package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PrefetchData contains page fault data for prefetch mapping.
type PrefetchData struct {
	// PageEntries contains metadata for each block index
	PageEntries map[uint64]block.PageEntry
	// BlockSize is the size of each block in bytes
	BlockSize int64
}

type MemoryBackend interface {
	DiffMetadata(ctx context.Context) (*header.DiffMetadata, error)
	PrefetchData(ctx context.Context) (*PrefetchData, error)
	Prefault(ctx context.Context, offset int64, data []byte) error
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}
