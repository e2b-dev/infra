package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	// Disable unregisters the uffd from the memory mapping and returns the dirty pages.
	// It must be called after FC pause finished and before FC snapshot is created.
	Disable(ctx context.Context) (*block.Tracker, error)
	Mapping(ctx context.Context) (*memory.Mapping, error)

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}
