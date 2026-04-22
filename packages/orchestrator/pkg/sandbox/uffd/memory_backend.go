package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	DiffMetadata(ctx context.Context, f *fc.Process) (*header.DiffMetadata, error)
	PrefetchData(ctx context.Context) (block.PrefetchData, error)
	Prefault(ctx context.Context, offset int64, data []byte) error
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
	Memfd(ctx context.Context) *block.Memfd
}
