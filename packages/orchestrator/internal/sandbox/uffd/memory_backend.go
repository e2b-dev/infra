package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	DiffMetadata(ctx context.Context) (*header.DiffMetadata, error)
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
	// GetPageFaultTrace returns a map of timestamp (unix nano) to offset for all page faults.
	GetPageFaultTrace() map[int64]int64
}
