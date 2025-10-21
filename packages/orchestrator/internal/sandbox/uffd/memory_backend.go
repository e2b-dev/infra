package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	Dirty(ctx context.Context) (*block.Tracker, error)
	// Disable switch the uffd to start serving empty pages.
	Disable(ctx context.Context) error
	Mapping(ctx context.Context) (*memory.Mapping, error)

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}
