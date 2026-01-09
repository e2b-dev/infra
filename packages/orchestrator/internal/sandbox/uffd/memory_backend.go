package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/trace"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	DiffMetadata(ctx context.Context) (*header.DiffMetadata, error)
	Prefault(ctx context.Context, offset int64, data []byte) error
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
	// SetTraceEnabled enables or disables page fault tracing.
	SetTraceEnabled(enabled bool)
	// GetPageFaultTrace returns page fault events (timestamp, offset, durations).
	GetPageFaultTrace() []trace.Event
}
