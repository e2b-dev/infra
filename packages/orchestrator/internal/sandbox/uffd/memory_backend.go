package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	// Dirty waits for the current requests to finish and returns the dirty pages.
	//
	// It *MUST* only be called after the sandbox was successfully paused and the snapshot create endpoint returned as these can still write to the memory.
	Dirty(ctx context.Context) (*block.Tracker, error)

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}
